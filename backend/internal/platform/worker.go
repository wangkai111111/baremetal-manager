package platform

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"baremetal-manager/backend/internal/model"
	"baremetal-manager/backend/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type operation struct {
	ID, TenantID, ProjectID, AssetID int64
	Kind, Status, WorkerID           string
}
type outbox struct{ ID, OperationID int64 }
type device struct {
	ID                                               int64
	BMCAddress, BMCUser, BMCSecretKeyID, BMCProtocol string
	BMCSecretCiphertext                              []byte
	BMCPort                                          int
	BMCTLSVerify                                     bool
}

func (a *App) action(c *gin.Context) {
	var in struct {
		Action         string `json:"action"`
		IdempotencyKey string `json:"idempotency_key"`
	}
	if c.ShouldBindJSON(&in) != nil {
		fail(c, 400, "invalid action")
		return
	}
	kind := map[string]string{"on": "power_on", "off": "power_off", "restart": "power_cycle", "power_on": "power_on", "power_off": "power_off", "power_cycle": "power_cycle", "collect": "collect", "inspect": "collect"}[in.Action]
	if kind == "" {
		fail(c, 501, "operation is not supported")
		return
	}
	permission := "bmc:operate"
	if kind == "collect" {
		permission = "assets:operate"
	}
	if !a.store.allowed(contextScope(c), permission) {
		fail(c, 403, "permission denied")
		return
	}
	a.submit(c, kind, in.IdempotencyKey)
}
func (a *App) collect(c *gin.Context) { a.submit(c, "collect", c.GetHeader("Idempotency-Key")) }
func (a *App) submit(c *gin.Context, kind, key string) {
	if key == "" {
		key = c.GetHeader("Idempotency-Key")
	}
	if key == "" {
		key = uuid.NewString()
	}
	if len(key) > 128 {
		fail(c, 400, "idempotency key too long")
		return
	}
	op, e := a.store.enqueue(c.Request.Context(), contextScope(c), number(c.Param("id")), kind, key)
	if e != nil {
		fail(c, 409, "operation conflict or asset unavailable")
		return
	}
	c.JSON(202, gin.H{"code": 0, "data": gin.H{"id": op.ID, "status": op.Status, "kind": op.Kind}, "message": "accepted"})
}
func (s *store) enqueue(ctx context.Context, sc scope, asset int64, kind, key string) (operation, error) {
	var op operation
	e := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if e := lockScope(tx, sc); e != nil {
			return e
		}
		var d device
		if e := queryScope(tx, resource{table: "assets"}, sc).Where("id=? AND status<>'retired'", asset).Take(&d).Error; e != nil {
			return e
		}
		e := queryScope(tx, resource{table: "operations"}, sc).Where("idempotency_key=?", key).Take(&op).Error
		if e == nil {
			if op.AssetID != asset || op.Kind != kind {
				return errors.New("idempotency conflict")
			}
			return nil
		}
		if !errors.Is(e, gorm.ErrRecordNotFound) {
			return e
		}
		op = operation{TenantID: sc.Tenant, ProjectID: sc.Project, AssetID: asset, Kind: kind, Status: "pending"}
		row := map[string]any{"tenant_id": sc.Tenant, "project_id": sc.Project, "asset_id": asset, "user_id": sc.User, "kind": kind, "idempotency_key": key, "status": "pending", "max_attempts": 1, "timeout_seconds": 60}
		if e = tx.Table("operations").Clauses(clause.Returning{Columns: []clause.Column{{Name: "id"}}}).Create(&row).Error; e != nil {
			return e
		}
		op.ID = asInt(row["id"])
		if e = tx.Table("task_outbox").Create(map[string]any{"tenant_id": sc.Tenant, "project_id": sc.Project, "operation_id": op.ID, "event_key": key, "event_type": "operation.execute"}).Error; e != nil {
			return e
		}
		return s.audit(tx, sc, "submit", "operations", op.ID)
	})
	return op, e
}
func (a *App) Worker(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	slots := make(chan struct{}, 4)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			for i := 0; i < 4; i++ {
				select {
				case slots <- struct{}{}:
					go func() { defer func() { <-slots }(); a.runOne(ctx) }()
				default:
				}
			}
		}
	}
}
func (a *App) claim(ctx context.Context) (operation, outbox, error) {
	var op operation
	var event outbox
	e := a.store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		e := tx.Raw("SELECT id,operation_id FROM task_outbox WHERE deleted_at IS NULL AND ((status='pending' AND available_at<=now()) OR (status='publishing' AND locked_until<now())) ORDER BY id FOR UPDATE SKIP LOCKED LIMIT 1").Scan(&event).Error
		if e != nil {
			return e
		}
		if event.ID == 0 {
			return gorm.ErrRecordNotFound
		}
		if e = tx.Table("operations").Clauses(clause.Locking{Strength: "UPDATE"}).Where("id=?", event.OperationID).Take(&op).Error; e != nil {
			return e
		}
		// 过期 claim 说明先前执行结果不明；任何动作都不自动重放。
		if op.Status != "pending" {
			if op.Status == "running" {
				if e = tx.Table("operations").Where("id=?", op.ID).Updates(map[string]any{"status": "unknown", "completed_at": time.Now(), "error": "worker claim expired; manual reconciliation required"}).Error; e != nil {
					return e
				}
				if e = operationLog(tx, op, 2, "warning", "claim expired; execution was not retried"); e != nil {
					return e
				}
			}
			if e = tx.Table("task_outbox").Where("id=?", event.ID).Updates(map[string]any{"status": "published", "published_at": time.Now(), "locked_until": nil}).Error; e != nil {
				return e
			}
			event.ID = 0
			return nil
		}
		var lease struct{ ID int64 }
		e = tx.Raw("INSERT INTO device_leases (tenant_id,project_id,asset_id,operation_id,holder_id,expires_at) VALUES (?,?,?,?,?,now()+interval '120 seconds') ON CONFLICT (tenant_id,project_id,asset_id) DO UPDATE SET operation_id=EXCLUDED.operation_id,holder_id=EXCLUDED.holder_id,fencing_token=device_leases.fencing_token+1,heartbeat_at=now(),expires_at=EXCLUDED.expires_at,deleted_at=NULL WHERE device_leases.expires_at<now() RETURNING id", op.TenantID, op.ProjectID, op.AssetID, op.ID, a.node).Scan(&lease).Error
		if e != nil {
			return e
		}
		if lease.ID == 0 {
			return tx.Table("task_outbox").Where("id=?", event.ID).Update("available_at", time.Now().Add(3*time.Second)).Error
		}
		if e = tx.Table("task_outbox").Where("id=?", event.ID).Updates(map[string]any{"status": "publishing", "locked_until": time.Now().Add(120 * time.Second), "attempts": gorm.Expr("attempts+1")}).Error; e != nil {
			return e
		}
		if e = tx.Table("operations").Where("id=? AND status='pending'", op.ID).Updates(map[string]any{"status": "running", "started_at": time.Now(), "worker_id": a.node, "attempts": gorm.Expr("attempts+1")}).Error; e != nil {
			return e
		}
		op.Status = "running"
		op.WorkerID = a.node
		return operationLog(tx, op, 1, "info", "device operation started")
	})
	return op, event, e
}
func operationLog(tx *gorm.DB, op operation, sequence int, level, message string) error {
	return tx.Table("operation_logs").Create(map[string]any{"tenant_id": op.TenantID, "project_id": op.ProjectID, "operation_id": op.ID, "sequence": sequence, "level": level, "message": message}).Error
}
func (a *App) loadDevice(ctx context.Context, sc scope, id int64) (model.Asset, device, error) {
	var d device
	e := queryScope(a.store.db.WithContext(ctx), resource{table: "assets"}, sc).Select("id,host(bmc_address) AS bmc_address,bmc_user,bmc_secret_ciphertext,bmc_secret_key_id,bmc_port,bmc_protocol,bmc_tls_verify").Where("id=? AND status<>'retired'", id).Take(&d).Error
	if e != nil {
		return model.Asset{}, d, e
	}
	if d.BMCSecretKeyID != "v1" {
		return model.Asset{}, d, errors.New("unsupported credential key")
	}
	secret, e := service.DecryptCredential(string(d.BMCSecretCiphertext))
	if e != nil {
		return model.Asset{}, d, e
	}
	return model.Asset{BMCAddress: d.BMCAddress, BMCUser: d.BMCUser, BMCSecret: secret}, d, nil
}
func (a *App) runOne(parent context.Context) {
	op, event, e := a.claim(parent)
	if e != nil || event.ID == 0 || op.WorkerID != a.node || op.Status != "running" {
		return
	}
	ctx, cancel := context.WithTimeout(parent, 60*time.Second)
	defer cancel()
	sc := scope{Tenant: op.TenantID, Project: op.ProjectID}
	asset, d, e := a.loadDevice(ctx, sc, op.AssetID)
	var snapshot json.RawMessage
	started := false
	if e != nil {
		// Database and credential errors are not safe to persist verbatim.
		e = errors.New("BMC device or credentials unavailable")
	} else {
		var target *targetBMC
		target, e = newTargetBMC(d, asset.BMCSecret)
		if e == nil {
			switch op.Kind {
			case "collect":
				snapshot, e = target.collect(ctx)
			case "power_on", "power_off", "power_cycle":
				started, e = target.power(ctx, op.Kind)
			default:
				e = errors.New("operation unsupported")
			}
		}
	}
	status, message, level := operationOutcome(op.Kind, started, e)
	finishCtx, stop := context.WithTimeout(context.Background(), 10*time.Second)
	defer stop()
	a.store.db.WithContext(finishCtx).Transaction(func(tx *gorm.DB) error {
		var lease int64
		if e := tx.Raw("SELECT id FROM device_leases WHERE operation_id=? AND holder_id=? AND expires_at>now() FOR UPDATE", op.ID, a.node).Scan(&lease).Error; e != nil {
			return e
		}
		if lease == 0 {
			return errors.New("lease lost")
		}
		updates := map[string]any{"status": status, "completed_at": time.Now()}
		if e != nil {
			updates["error"] = message
		}
		res := tx.Table("operations").Where("id=? AND status='running' AND worker_id=?", op.ID, a.node).Updates(updates)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected != 1 {
			return errors.New("claim lost")
		}
		if op.Kind != "collect" && (status == "succeeded" || status == "unknown") {
			state := "unknown"
			if status == "succeeded" {
				state = completedPowerState(op.Kind)
			}
			if e := tx.Table("assets").Where("id=? AND tenant_id=? AND project_id=?", op.AssetID, op.TenantID, op.ProjectID).Update("power_state", state).Error; e != nil {
				return e
			}
		}
		if status != "succeeded" && op.Kind == "collect" {
			if e := tx.Table("assets").Where("id=? AND tenant_id=? AND project_id=?", op.AssetID, op.TenantID, op.ProjectID).Update("collection_error", message).Error; e != nil {
				return e
			}
		}
		if status == "succeeded" && op.Kind == "collect" {
			var obj map[string]any
			if json.Unmarshal(snapshot, &obj) == nil {
				safe := map[string]any{}
				for _, k := range []string{"Manufacturer", "Model", "SerialNumber", "PowerState", "BiosVersion"} {
					if v, ok := obj[k].(string); ok {
						safe[k] = v
					}
				}
				for _, k := range []string{"ProcessorSummary", "MemorySummary"} {
					if v, ok := obj[k].(map[string]any); ok {
						safe[k] = v
					}
				}
				b, _ := json.Marshal(safe)
				assetUpdates := map[string]any{"redfish_snapshot": gorm.Expr("?::jsonb", string(b)), "collected_at": time.Now(), "collection_error": nil}
				switch safe["PowerState"] {
				case "On":
					assetUpdates["power_state"] = "on"
				case "Off":
					assetUpdates["power_state"] = "off"
				default:
					assetUpdates["power_state"] = "unknown"
				}
				if e := tx.Table("assets").Where("id=? AND tenant_id=? AND project_id=?", op.AssetID, op.TenantID, op.ProjectID).Updates(assetUpdates).Error; e != nil {
					return e
				}
			}
		}
		if e := operationLog(tx, op, 2, level, message); e != nil {
			return e
		}
		if e := tx.Table("task_outbox").Where("id=? AND status='publishing'", event.ID).Updates(map[string]any{"status": "published", "published_at": time.Now(), "locked_until": nil}).Error; e != nil {
			return e
		}
		return tx.Table("device_leases").Where("id=? AND holder_id=?", lease, a.node).Updates(map[string]any{"heartbeat_at": time.Now().Add(-time.Second), "expires_at": time.Now()}).Error
	})
}

// Scheduler 只清理到期会话；不为未接入的装机和固件执行器伪造状态。
func (a *App) Scheduler(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.store.db.WithContext(ctx).Table("sol_sessions").Where("ended_at IS NULL AND expires_at<=now()").Updates(map[string]any{"ended_at": time.Now(), "end_reason": "expired"})
		}
	}
}
