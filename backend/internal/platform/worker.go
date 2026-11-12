package platform

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"baremetal-manager/backend/internal/model"
	"baremetal-manager/backend/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type operation struct {
	ID, TenantID, ProjectID, AssetID, UserID int64
	Kind, Status, WorkerID                   string
	Automatic                                bool
}
type outbox struct{ ID, OperationID int64 }
type device struct {
	ID                                               int64
	BMCAddress, BMCUser, BMCSecretKeyID, BMCProtocol string
	BMCSecretCiphertext                              []byte
	BMCPort                                          int
	BMCTLSVerify                                     bool
}

func (a *App) batchActions(c *gin.Context) {
	if !a.store.allowed(contextScope(c), "bmc:operate") {
		fail(c, 403, "permission denied")
		return
	}
	var in struct {
		AssetIDs       []int64 `json:"asset_ids"`
		Action         string  `json:"action"`
		IdempotencyKey string  `json:"idempotency_key"`
	}
	if c.ShouldBindJSON(&in) != nil || len(in.AssetIDs) == 0 || len(in.AssetIDs) > 500 {
		fail(c, 400, "asset_ids 不能为空且不能超过500个")
		return
	}
	kind := map[string]string{"on": "power_on", "off": "power_off", "restart": "power_cycle", "power_on": "power_on", "power_off": "power_off", "power_cycle": "power_cycle"}[in.Action]
	if kind == "" {
		fail(c, 400, "operation is not supported")
		return
	}
	key := in.IdempotencyKey
	if key == "" {
		key = c.GetHeader("Idempotency-Key")
	}
	if key == "" {
		key = uuid.NewString()
	}
	if len(key) > 100 {
		fail(c, 400, "idempotency key too long")
		return
	}
	results := make([]gin.H, 0, len(in.AssetIDs))
	for _, id := range in.AssetIDs {
		op, err := a.store.enqueue(c.Request.Context(), contextScope(c), id, kind, fmt.Sprintf("%s-%d", key, id))
		if err != nil {
			results = append(results, gin.H{"asset_id": id, "status": "failed", "reason": "资产不存在、无权访问或已有冲突操作"})
			continue
		}
		results = append(results, gin.H{"asset_id": id, "status": "accepted", "operation_id": op.ID, "kind": op.Kind})
	}
	c.JSON(202, gin.H{"code": 0, "data": gin.H{"results": results}, "message": "批量操作已逐台提交"})
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

type collectionDTO struct {
	Partial                   bool                           `json:"partial"`
	Components                map[string][]hardwareComponent `json:"components,omitempty"`
	ComponentStates           map[string]componentState      `json:"component_states,omitempty"`
	Supported                 bool                           `json:"supported"`
	ComponentDetailsSupported bool                           `json:"component_details_supported"`
	Name                      string                         `json:"name"`
	Manufacturer              string                         `json:"manufacturer"`
	Model                     string                         `json:"model"`
	SerialNumber              string                         `json:"serial_number"`
	CPUCount                  int                            `json:"cpu_count"`
	CPUModel                  string                         `json:"cpu_model"`
	MemoryGiB                 float64                        `json:"memory_gib"`
	BIOSVersion               string                         `json:"bios_version"`
	CollectedAt               *time.Time                     `json:"collected_at"`
	CollectionError           string                         `json:"collection_error"`
	Status                    string                         `json:"status"`
	OperationID               int64                          `json:"operation_id,omitempty"`
	Health                    string                         `json:"health"`
	ConsecutiveFailures       int                            `json:"consecutive_failures"`
	LastAttemptAt             *time.Time                     `json:"last_attempt_at"`
	NextCollectionAt          *time.Time                     `json:"next_collection_at"`
	MonitoringEnabled         bool                           `json:"monitoring_enabled"`
}

type collectionRow struct {
	BMCProtocol                         string
	RedfishSnapshot                     json.RawMessage
	CollectedAt                         *time.Time
	CollectionError                     string
	MonitorHealth                       string
	MonitorConsecutiveFailures          int
	MonitorLastAttemptAt, MonitorNextAt *time.Time
	MonitoringEnabled                   bool
}

func collectionSummary(row collectionRow) collectionDTO {
	dto := collectionDTO{Supported: row.BMCProtocol == "redfish", Status: "not_collected", Health: row.MonitorHealth, ConsecutiveFailures: row.MonitorConsecutiveFailures, LastAttemptAt: row.MonitorLastAttemptAt, MonitoringEnabled: row.MonitoringEnabled}
	if dto.Health == "" {
		dto.Health = "unknown"
	}
	if dto.MonitoringEnabled {
		dto.NextCollectionAt = row.MonitorNextAt
	}
	if !dto.Supported {
		dto.Status = "unsupported"
	}
	if row.CollectionError != "" {
		dto.Status = "failed"
		dto.CollectionError = "硬件采集失败，请检查 BMC 配置、连接及任务状态"
	}
	s, err := parseComputerSystemSummary(row.RedfishSnapshot)
	if err != nil {
		return dto
	}
	dto.ComponentDetailsSupported = s.ComponentDetailsSupported
	dto.Partial, dto.Components, dto.ComponentStates = s.Partial, s.Components, s.ComponentStates
	dto.CollectedAt = row.CollectedAt
	if dto.Supported && dto.CollectionError == "" && row.CollectedAt != nil {
		dto.Status = "succeeded"
	}
	dto.Name, dto.Manufacturer, dto.Model, dto.SerialNumber = s.Name, s.Manufacturer, s.Model, s.SerialNumber
	dto.BIOSVersion = s.BiosVersion
	if s.ProcessorSummary != nil {
		dto.CPUCount, dto.CPUModel = s.ProcessorSummary.Count, s.ProcessorSummary.Model
	}
	if s.MemorySummary != nil {
		dto.MemoryGiB = s.MemorySummary.TotalSystemMemoryGiB
	}
	return dto
}

func (a *App) collection(c *gin.Context) {
	var row collectionRow
	if err := queryScope(a.store.db.WithContext(c.Request.Context()), resource{table: "assets"}, contextScope(c)).
		Select(`bmc_protocol,redfish_snapshot,collected_at,collection_error,monitor_health,monitor_consecutive_failures,monitor_last_attempt_at,monitor_next_at,
		bmc_protocol='redfish' AND status<>'retired' AND NOT EXISTS (SELECT 1 FROM asset_monitor_configs mc WHERE mc.tenant_id=assets.tenant_id AND mc.project_id=assets.project_id AND mc.asset_id=assets.id AND mc.deleted_at IS NULL AND NOT mc.enabled) AS monitoring_enabled`).Where("id=?", number(c.Param("id"))).Take(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			fail(c, 404, "asset not found")
		} else {
			fail(c, 500, "collection summary unavailable")
		}
		return
	}
	dto := collectionSummary(row)
	var latest operation
	err := queryScope(a.store.db.WithContext(c.Request.Context()), resource{table: "operations"}, contextScope(c)).
		Select("id,status").Where("asset_id=? AND kind='collect' AND status<>'cancelled'", number(c.Param("id"))).Order("id DESC").Take(&latest).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		fail(c, 500, "collection summary unavailable")
		return
	}
	if err == nil {
		dto.OperationID = latest.ID
		// A legacy empty snapshot must never be presented as successful collection.
		if latest.Status != "succeeded" || dto.CollectedAt != nil {
			dto.Status = latest.Status
		}
		if latest.Status == "failed" || latest.Status == "unknown" {
			dto.CollectionError = "硬件采集失败或结果未确认，请检查 BMC 配置、连接及任务状态"
		}
	}
	ok(c, dto)
}

func collectionAssetUpdates(snapshot json.RawMessage) (map[string]any, error) {
	s, err := parseComputerSystemSummary(snapshot)
	if err != nil {
		return nil, err
	}
	b, err := json.Marshal(s)
	if err != nil {
		return nil, err
	}
	updates := map[string]any{"redfish_snapshot": gorm.Expr("?::jsonb", string(b)), "collected_at": time.Now(), "collection_error": nil, "power_state": "unknown"}
	for key, value := range map[string]string{"manufacturer": s.Manufacturer, "model": s.Model, "serial_number": s.SerialNumber} {
		if value = strings.TrimSpace(value); value != "" {
			updates[key] = value
		}
	}
	switch s.PowerState {
	case "On":
		updates["power_state"] = "on"
	case "Off":
		updates["power_state"] = "off"
	}
	return updates, nil
}

var errCollectionUnsupported = errors.New("hardware collection requires Redfish protocol")

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
		if errors.Is(e, errCollectionUnsupported) {
			fail(c, 422, "IPMI 暂不支持硬件采集，请使用 Redfish；电源操作不受影响")
			return
		}
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
		if e := queryScope(tx, resource{table: "assets"}, sc).Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND project_id=? AND id=? AND status<>'retired' AND COALESCE(subscription_state, 'legacy') <> 'reclaim_pending'", sc.Tenant, sc.Project, asset).Take(&d).Error; e != nil {
			return e
		}
		if kind == "collect" && d.BMCProtocol != "redfish" {
			return errCollectionUnsupported
		}
		e := queryScope(tx, resource{table: "operations"}, sc).Where("idempotency_key=?", key).Take(&op).Error
		if e == nil {
			if op.AssetID != asset || op.Kind != kind || op.UserID != sc.User {
				return errors.New("idempotency conflict")
			}
			return nil
		}
		if !errors.Is(e, gorm.ErrRecordNotFound) {
			return e
		}
		if kind == "collect" {
			e = tx.Table("operations").Where("tenant_id=? AND project_id=? AND asset_id=? AND deleted_at IS NULL AND kind='collect' AND status IN ('pending','running','retry_wait')", sc.Tenant, sc.Project, asset).Order("id").Take(&op).Error
			if e == nil {
				return nil
			}
			if !errors.Is(e, gorm.ErrRecordNotFound) {
				return e
			}
			if e = tx.Table("assets").Where("id=?", asset).Update("monitor_next_at", gorm.Expr("now()+interval '5 minutes'")).Error; e != nil {
				return e
			}
		}
		op = operation{TenantID: sc.Tenant, ProjectID: sc.Project, AssetID: asset, Kind: kind, Status: "pending"}
		timeout := 60
		if kind == "collect" {
			timeout = 100
		}
		row := map[string]any{"tenant_id": sc.Tenant, "project_id": sc.Project, "asset_id": asset, "user_id": sc.User, "kind": kind, "idempotency_key": key, "status": "pending", "max_attempts": 1, "timeout_seconds": timeout}
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
	notificationSlots := make(chan struct{}, 4)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			for i := 0; i < 4; i++ {
				select {
				case notificationSlots <- struct{}{}:
					go func() { defer func() { <-notificationSlots }(); a.deliverNotification(ctx) }()
				default:
				}
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
		var assetState struct{ SubscriptionState string }
		if e = tx.Raw("SELECT a.subscription_state FROM assets a JOIN operations o ON o.asset_id=a.id WHERE o.id=? FOR UPDATE OF a", event.OperationID).Scan(&assetState).Error; e != nil {
			return e
		}
		if e = tx.Table("operations").Clauses(clause.Locking{Strength: "UPDATE"}).Where("id=?", event.OperationID).Take(&op).Error; e != nil {
			return e
		}
		if assetState.SubscriptionState == "reclaim_pending" && op.Kind != "power_off" && op.Status == "pending" {
			if e = tx.Table("operations").Where("id=?", op.ID).Update("status", "cancelled").Error; e != nil {
				return e
			}
			op.Status = "cancelled"
		}
		// 过期 claim 说明先前执行结果不明；任何动作都不自动重放。
		if op.Status != "pending" {
			if op.Status == "running" {
				if e = tx.Table("operations").Where("id=?", op.ID).Updates(map[string]any{"status": "unknown", "completed_at": time.Now(), "error": "worker claim expired; manual reconciliation required"}).Error; e != nil {
					return e
				}
				if op.Kind == "collect" {
					if e = finishCollection(tx, op, "unknown", "collection worker claim expired", nil); e != nil {
						return e
					}
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
	budget := 60 * time.Second
	if op.Kind == "collect" {
		budget = collectionDeadline + 10*time.Second
	}
	ctx, cancel := context.WithTimeout(parent, budget)
	defer cancel()
	sc := scope{Tenant: op.TenantID, Project: op.ProjectID, Admin: true}
	var asset model.Asset
	var d device
	var skipped bool
	if op.Automatic {
		var enabled bool
		e = a.store.db.WithContext(ctx).Raw(`SELECT EXISTS (SELECT 1 FROM assets a WHERE a.id=? AND a.tenant_id=? AND a.project_id=? AND `+monitoredAssetSQL+`)`, op.AssetID, op.TenantID, op.ProjectID).Scan(&enabled).Error
		skipped = e == nil && !enabled
	}
	if e == nil && !skipped {
		asset, d, e = a.loadDevice(ctx, sc, op.AssetID)
	}
	var snapshot json.RawMessage
	started := false
	if e != nil {
		// Database and credential errors are not safe to persist verbatim.
		e = errors.New("BMC device or credentials unavailable")
	} else if !skipped {
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
	if skipped {
		status, message, level = "cancelled", "automatic monitoring disabled or asset inactive", "info"
	}
	finishCtx, stop := context.WithTimeout(context.Background(), 10*time.Second)
	defer stop()
	finishErr := a.store.db.WithContext(finishCtx).Transaction(func(tx *gorm.DB) error {
		var claimed outbox
		if err := tx.Table("task_outbox").Select("id").Clauses(clause.Locking{Strength: "UPDATE"}).Where("id=? AND status='publishing' AND locked_until>now()", event.ID).Take(&claimed).Error; err != nil {
			return err
		}
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
		if op.Kind == "collect" && !skipped {
			if err := finishCollection(tx, op, status, message, snapshot); err != nil {
				return err
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
	if finishErr != nil {
		log.Printf("operation finalization failed: operation_id=%d asset_id=%d tenant_id=%d project_id=%d; database or lease failure", op.ID, op.AssetID, op.TenantID, op.ProjectID)
	}
}

func (a *App) Scheduler(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for ctx.Err() == nil {
		a.store.db.WithContext(ctx).Table("sol_sessions").Where("ended_at IS NULL AND expires_at<=now()").Updates(map[string]any{"ended_at": time.Now(), "end_reason": "expired"})
		if err := a.store.scheduleCollections(ctx); err != nil && ctx.Err() == nil {
			log.Print("Redfish collection scheduling failed")
		}
		// 传感器历史保留期清理（内部限流为每小时一次）。
		a.purgeSensorHistory()
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
