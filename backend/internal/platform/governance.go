package platform

// ============================================================
// 平台治理（运营模式）：租户审核 + 风控管理
// ============================================================
//
// 注册路由（routes.go，均限运营模式 + 仅平台管理员）：
// GET    /api/v1/projects/:projectId/admin/tenants                 → listAdminTenants       → operations:read
// PUT    /api/v1/projects/:projectId/admin/tenants/:id/:transition → transitionAdminTenant  → operations:write
// GET    /api/v1/projects/:projectId/admin/risk-items              → listRiskItems          → operations:read
// POST   /api/v1/projects/:projectId/admin/risk-items/:kind/:action → handleRiskItem        → operations:write
//
// 租户状态机（011_governance_phase56.sql 扩展 tenants_status_check）：
//
//	pending  --approve--> active      --disable--> disabled
//	pending  --reject --> rejected                   --enable--> active
//
// rejected 为终态：不自愈、不隐藏，由运营人员复核后引导重新注册，避免误恢复高风险租户。
// 风控处置一律写审计（recordAudit 带来源 IP / User-Agent / request_id）。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// tenantTransitions 是租户审核状态机的唯一权威定义：状态 → 允许的流转动作。
var tenantTransitions = map[string]map[string]string{
	"pending":  {"approve": "active", "reject": "rejected"},
	"active":   {"disable": "disabled"},
	"disabled": {"enable": "active"},
	"rejected": {},
}

const adminTenantColumns = `t.id, t.name, t.code, t.status, t.created_at, t.updated_at,
 t.reviewed_at, t.review_note, t.reviewed_by, reviewer.name AS reviewed_by_name,
 tp.contact_name, tp.contact_phone, tp.contact_email, tp.company_name,
 COALESCE(tp.verified,false) AS verified,
 (SELECT COUNT(*) FROM users u JOIN tenant_memberships tm ON tm.tenant_id=t.id AND tm.user_id=u.id AND tm.deleted_at IS NULL
  WHERE u.deleted_at IS NULL) AS user_count,
 (SELECT COUNT(*) FROM projects p WHERE p.tenant_id=t.id AND p.deleted_at IS NULL) AS project_count,
 (SELECT COUNT(*) FROM assets a WHERE a.tenant_id=t.id AND a.deleted_at IS NULL) AS asset_count`

// requirePlatformAdmin 治理端点二次校验：模式门禁已由路由中间件处理，此处只判定平台管理员身份。
func requirePlatformAdmin(c *gin.Context) (scope, bool) {
	sc := contextScope(c)
	if !sc.Admin {
		fail(c, 403, "需要平台管理员权限")
		return sc, false
	}
	return sc, true
}

// listAdminTenants 返回租户审核队列，支持状态、关键字筛选与分页。
func (a *App) listAdminTenants(c *gin.Context) {
	if _, pass := requirePlatformAdmin(c); !pass {
		return
	}
	q := a.store.db.Table("tenants t").
		Joins("LEFT JOIN tenant_profiles tp ON tp.tenant_id=t.id AND tp.deleted_at IS NULL").
		Joins("LEFT JOIN users reviewer ON reviewer.id=t.reviewed_by").
		Where("t.deleted_at IS NULL")
	if status := strings.TrimSpace(c.Query("status")); status != "" {
		if _, valid := tenantTransitions[status]; !valid && status != "all" {
			fail(c, 400, "无效的租户状态筛选，仅支持 pending/active/disabled/rejected")
			return
		}
		if status != "all" {
			q = q.Where("t.status=?", status)
		}
	}
	if search := strings.TrimSpace(c.Query("search")); search != "" {
		like := "%" + search + "%"
		q = q.Where("(t.name ILIKE ? OR t.code ILIKE ? OR tp.company_name ILIKE ? OR tp.contact_name ILIKE ? OR tp.contact_phone ILIKE ? OR tp.contact_email ILIKE ?)",
			like, like, like, like, like, like)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		fail(c, 500, "租户查询失败")
		return
	}
	p, n := page(c)
	var items []map[string]any
	if err := q.Select(adminTenantColumns).Order("CASE t.status WHEN 'pending' THEN 0 ELSE 1 END, t.id DESC").
		Limit(n).Offset((p - 1) * n).Scan(&items).Error; err != nil {
		fail(c, 500, "租户查询失败")
		return
	}
	for _, item := range items {
		delete(item, "reviewed_by")
	}
	ok(c, gin.H{"items": items, "total": total, "page": p, "page_size": n})
}

// transitionAdminTenant 执行租户审核流转：approve|reject|disable|enable。
func (a *App) transitionAdminTenant(c *gin.Context) {
	sc, pass := requirePlatformAdmin(c)
	if !pass {
		return
	}
	id := number(c.Param("id"))
	transition := strings.TrimSpace(c.Param("transition"))
	if id <= 0 {
		fail(c, 400, "无效的租户ID")
		return
	}
	var in struct {
		Note string `json:"note"`
	}
	if c.Request.ContentLength > 0 {
		if c.ShouldBindJSON(&in) != nil {
			fail(c, 400, "无效的请求体")
			return
		}
	}
	note := strings.TrimSpace(in.Note)
	if len(note) > 512 {
		fail(c, 400, "审核意见长度不能超过512字符")
		return
	}
	if transition == "reject" && note == "" {
		fail(c, 400, "拒绝租户时必须填写审核意见")
		return
	}
	var current struct {
		ID     int64
		Name   string
		Status string
	}
	if err := a.store.db.Table("tenants").Select("id,name,status").
		Where("id=? AND deleted_at IS NULL", id).Take(&current).Error; err != nil {
		fail(c, 404, "租户不存在")
		return
	}
	target, allowed := tenantTransitions[current.Status][transition]
	if !allowed {
		fail(c, 400, fmt.Sprintf("租户当前状态 %s 不允许执行 %s", current.Status, transition))
		return
	}
	// 默认租户与唯一平台管理员所属租户禁止停用，否则平台自锁。
	if target == "disabled" || target == "rejected" {
		var admins int64
		a.store.db.Table("users u").
			Joins("JOIN tenant_memberships tm ON tm.user_id=u.id AND tm.tenant_id=?", id).
			Where("u.is_platform_admin AND u.deleted_at IS NULL AND u.status='active'").Count(&admins)
		if admins > 0 {
			fail(c, 409, "该租户包含平台管理员，停用或拒绝将导致平台不可管理")
			return
		}
	}
	updates := map[string]any{
		"status":      target,
		"reviewed_at": time.Now(),
		"reviewed_by": sc.User,
		"update_by":   sc.User,
	}
	if note != "" {
		updates["review_note"] = note
	} else {
		updates["review_note"] = nil
	}
	meta := requestMeta(c, sc.User)
	e := a.store.db.Transaction(func(tx *gorm.DB) error {
		res := tx.Table("tenants").Where("id=? AND status=? AND deleted_at IS NULL", id, current.Status).Updates(updates)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected != 1 {
			return errors.New("租户状态已被其他操作变更，请刷新后重试")
		}
		return a.store.recordAudit(tx, sc, meta, "tenant:"+transition, "tenants", id, "success")
	})
	if e != nil {
		fail(c, 409, e.Error())
		return
	}
	ok(c, gin.H{"id": id, "status": target, "reviewed_at": time.Now().UTC(), "review_note": note})
}

// ============================================================
// A3 风控管理
// ============================================================

// riskItemSQL 归一化四类风险口径：欠费账单、长期待支付订单、余额不足账户、待回收资产。
// 口径与既有业务查询保持一致：账单逾期含 unpaid 且已过 due_date；订单 pending 超阈值天数；
// 账户余额（含信用额度）不可覆盖在途订单；资产回收态沿用 subscriptions 的 reclaim_pending。
const riskItemSQL = `
SELECT * FROM (
 SELECT 'overdue_bill'::text AS kind, b.id AS ref_id, b.tenant_id AS tenant_id, t.name AS tenant_name,
   ('账单 #' || b.id::text || ' 逾期未付')::text AS title,
   b.total_amount AS amount, 'high'::text AS severity,
   COALESCE(b.due_date, b.created_at) AS detected_at,
   COALESCE(b.status,'unpaid') AS source_status,
   jsonb_build_object('period_start',b.period_start,'period_end',b.period_end,'due_date',b.due_date) AS detail
 FROM bills b JOIN tenants t ON t.id=b.tenant_id
 WHERE b.deleted_at IS NULL AND t.deleted_at IS NULL
   AND (b.status='overdue' OR (b.status='unpaid' AND b.due_date IS NOT NULL AND b.due_date < now()))
 UNION ALL
 SELECT 'pending_order', o.id, o.tenant_id, t.name,
   ('订单 #' || o.id::text || ' 长期待支付')::text, o.final_amount, 'medium',
   o.created_at, o.status,
   jsonb_build_object('type',o.type,'asset_id',o.asset_id,'order_no',o.id)
 FROM orders o JOIN tenants t ON t.id=o.tenant_id
 WHERE o.deleted_at IS NULL AND t.deleted_at IS NULL AND o.status='pending'
   AND o.created_at < now() - (? * interval '1 day')
 UNION ALL
 SELECT 'low_balance', ac.id, ac.tenant_id, t.name,
   ('账户余额不足：' || to_char(ac.balance,'FM999999990.00'))::text, ac.balance, 'critical',
   ac.updated_at, t.status,
   jsonb_build_object('balance',ac.balance,'frozen_amount',ac.frozen_amount,'credit_limit',ac.credit_limit,
     'open_orders',(SELECT COUNT(*) FROM orders o WHERE o.tenant_id=ac.tenant_id AND o.status='pending' AND o.deleted_at IS NULL))
 FROM accounts ac JOIN tenants t ON t.id=ac.tenant_id
 WHERE ac.deleted_at IS NULL AND t.deleted_at IS NULL AND t.status='active'
   AND (ac.balance < 0 OR (ac.balance + ac.credit_limit <= 0
     AND EXISTS (SELECT 1 FROM orders o WHERE o.tenant_id=ac.tenant_id AND o.status='pending' AND o.deleted_at IS NULL)))
 UNION ALL
 SELECT 'reclaim_asset', ast.id, ast.tenant_id, t.name,
   ('资产 #' || ast.id::text || ' 待回收')::text, NULL::numeric, 'high',
   COALESCE(ast.unsubscribed_at, ast.updated_at), ast.status,
   jsonb_build_object('asset_name',ast.name,'power_state',ast.power_state,'unsubscribed_at',ast.unsubscribed_at)
 FROM assets ast JOIN tenants t ON t.id=ast.tenant_id
 WHERE ast.deleted_at IS NULL AND t.deleted_at IS NULL AND ast.subscription_state='reclaim_pending'
) AS risk`

var riskKinds = map[string]bool{"overdue_bill": true, "pending_order": true, "low_balance": true, "reclaim_asset": true}

// riskActions 定义每种风险可执行的处置动作，超出范围一律业务错误。
var riskActions = map[string]map[string]bool{
	"overdue_bill":  {"suspend": true, "notify": true},
	"pending_order": {"suspend": true, "notify": true},
	"low_balance":   {"suspend": true, "notify": true},
	"reclaim_asset": {"reclaim": true, "notify": true},
}

func (a *App) riskPendingDays(c *gin.Context) int {
	days := int(number(c.DefaultQuery("pending_days", "7")))
	if days <= 0 {
		days = 7
	}
	if days > 365 {
		days = 365
	}
	return days
}

// listRiskItems 聚合返回平台级风险清单（跨租户，仅平台管理员可见）。
func (a *App) listRiskItems(c *gin.Context) {
	if _, pass := requirePlatformAdmin(c); !pass {
		return
	}
	days := a.riskPendingDays(c)
	conditions, args := []string{"1=1"}, []any{days}
	if kind := strings.TrimSpace(c.Query("kind")); kind != "" && kind != "all" {
		if !riskKinds[kind] {
			fail(c, 400, "无效的风险类型，仅支持 overdue_bill/pending_order/low_balance/reclaim_asset")
			return
		}
		conditions, args = append(conditions, "risk.kind=?"), append(args, kind)
	}
	if tenant := number(c.Query("tenant_id")); tenant > 0 {
		conditions, args = append(conditions, "risk.tenant_id=?"), append(args, tenant)
	}
	if severity := strings.TrimSpace(c.Query("severity")); severity != "" {
		conditions, args = append(conditions, "risk.severity=?"), append(args, severity)
	}
	if search := strings.TrimSpace(c.Query("search")); search != "" {
		conditions, args = append(conditions, "(risk.title ILIKE ? OR risk.tenant_name ILIKE ?)"), append(args, "%"+search+"%", "%"+search+"%")
	}
	where := strings.Join(conditions, " AND ")
	var total int64
	if err := a.store.db.Raw("SELECT COUNT(*) FROM ("+riskItemSQL+") AS risk WHERE "+where, args...).Scan(&total).Error; err != nil {
		fail(c, 500, "风险清单查询失败")
		return
	}
	p, n := page(c)
	pageArgs := append(append([]any{}, args...), n, (p-1)*n)
	var items []map[string]any
	if err := a.store.db.Raw("SELECT risk.* FROM ("+riskItemSQL+") AS risk WHERE "+where+" ORDER BY CASE risk.severity WHEN 'critical' THEN 0 WHEN 'high' THEN 1 ELSE 2 END, risk.detected_at DESC LIMIT ? OFFSET ?", pageArgs...).Scan(&items).Error; err != nil {
		fail(c, 500, "风险清单查询失败")
		return
	}
	for _, item := range items {
		if raw, present := item["detail"].(string); present {
			var detail any
			if json.Unmarshal([]byte(raw), &detail) == nil {
				item["detail"] = detail
			}
		}
	}
	ok(c, gin.H{"items": items, "total": total, "page": p, "page_size": n, "pending_days": days})
}

// handleRiskItem 对单条风险执行处置：suspend 停用租户、reclaim 提交资产回收关机、notify 触达租户。
func (a *App) handleRiskItem(c *gin.Context) {
	sc, pass := requirePlatformAdmin(c)
	if !pass {
		return
	}
	kind := strings.TrimSpace(c.Param("kind"))
	action := strings.TrimSpace(c.Param("action"))
	if !riskKinds[kind] {
		fail(c, 400, "无效的风险类型")
		return
	}
	if !riskActions[kind][action] {
		fail(c, 400, kind+" 不支持处置动作 "+action+"，仅支持 "+strings.Join(actionNames(riskActions[kind]), "/"))
		return
	}
	var in riskActionInput
	if c.ShouldBindJSON(&in) != nil || in.TargetID <= 0 {
		fail(c, 400, "必须提供 target_id")
		return
	}
	note := strings.TrimSpace(in.Note)
	if len(note) > 512 {
		fail(c, 400, "处置备注长度不能超过512字符")
		return
	}
	meta := requestMeta(c, sc.User)
	switch action {
	case "suspend":
		a.suspendRiskTenant(c, sc, meta, kind, in, note)
	case "reclaim":
		a.reclaimRiskAsset(c, sc, meta, in, note)
	case "notify":
		a.notifyRiskTenant(c, sc, meta, kind, in, note)
	}
}

func actionNames(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for name := range m {
		out = append(out, name)
	}
	return out
}

// riskActionInput 是风控处置端点的请求体。
type riskActionInput struct {
	TargetID int64  `json:"target_id"`
	TenantID int64  `json:"tenant_id"`
	Note     string `json:"note"`
}

// suspendRiskTenant 停用风险租户；项目侧门禁（project 中间件要求 tenants.status='active'）随即生效。
func (a *App) suspendRiskTenant(c *gin.Context, sc scope, meta auditMeta, kind string, in riskActionInput, note string) {
	tenantID, err := a.riskTenantID(kind, in.TargetID, in.TenantID)
	if err != nil {
		fail(c, 404, err.Error())
		return
	}
	var tenant struct {
		ID     int64
		Status string
	}
	if err := a.store.db.Table("tenants").Select("id,status").Where("id=? AND deleted_at IS NULL", tenantID).Take(&tenant).Error; err != nil {
		fail(c, 404, "租户不存在")
		return
	}
	if tenant.Status != "active" {
		fail(c, 409, "仅正常状态的租户可被停用，当前状态 "+tenant.Status)
		return
	}
	if note == "" {
		note = "风控自动停用：" + kind
	}
	e := a.store.db.Transaction(func(tx *gorm.DB) error {
		res := tx.Table("tenants").Where("id=? AND status='active' AND deleted_at IS NULL", tenantID).
			Updates(map[string]any{"status": "disabled", "reviewed_at": time.Now(), "reviewed_by": sc.User, "review_note": note, "update_by": sc.User})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected != 1 {
			return errors.New("租户状态已变更，请刷新后重试")
		}
		return a.store.recordAudit(tx, sc, meta, "risk:suspend", "tenants", tenantID, "success")
	})
	if e != nil {
		fail(c, 409, e.Error())
		return
	}
	ok(c, gin.H{"kind": kind, "action": "suspend", "tenant_id": tenantID, "target_id": in.TargetID, "status": "disabled", "note": note})
}

// reclaimRiskAsset 为待回收资产提交关机操作（真实执行通道），并在审计中留痕；不清盘、不删除资产。
func (a *App) reclaimRiskAsset(c *gin.Context, sc scope, meta auditMeta, in riskActionInput, note string) {
	var asset struct {
		ID      int64
		Tenant  int64  `gorm:"column:tenant_id"`
		Project int64  `gorm:"column:project_id"`
		State   string `gorm:"column:subscription_state"`
		Name    string
	}
	if err := a.store.db.Table("assets").Select("id,tenant_id,project_id,name,subscription_state").
		Where("id=? AND deleted_at IS NULL", in.TargetID).Take(&asset).Error; err != nil {
		fail(c, 404, "资产不存在")
		return
	}
	if asset.State != "reclaim_pending" {
		fail(c, 409, "该资产不处于待回收状态，当前状态 "+asset.State)
		return
	}
	if note == "" {
		note = "风控回收：提交安全关机，等待人工确认后续处置"
	}
	target := scope{Tenant: asset.Tenant, Project: asset.Project, User: sc.User, Admin: true}
	op, err := a.store.enqueue(c.Request.Context(), target, asset.ID, "power_off", fmt.Sprintf("risk-reclaim-%d-%d", asset.ID, time.Now().UnixNano()))
	outcome := "success"
	if err != nil {
		outcome = "failed"
	}
	if e := a.store.recordAuditNote(a.store.db, sc, meta, "risk:reclaim", "assets", asset.ID, outcome, gin.H{"note": note, "asset_name": asset.Name}); e != nil {
		fail(c, 500, "风控回收留痕失败")
		return
	}
	if err != nil {
		ok(c, gin.H{"kind": "reclaim_asset", "action": "reclaim", "asset_id": asset.ID, "asset_name": asset.Name, "status": "failed", "reason": "资产已有冲突操作或不可执行", "note": note})
		return
	}
	ok(c, gin.H{"kind": "reclaim_asset", "action": "reclaim", "asset_id": asset.ID, "asset_name": asset.Name, "operation_id": op.ID, "status": "queued", "note": note})
}

// notifyRiskTenant 通过租户已启用的告警渠道发送风控通知；无可用渠道时如实返回失败原因。
func (a *App) notifyRiskTenant(c *gin.Context, sc scope, meta auditMeta, kind string, in riskActionInput, note string) {
	tenantID, err := a.riskTenantID(kind, in.TargetID, in.TenantID)
	if err != nil {
		fail(c, 404, err.Error())
		return
	}
	subject := "平台风控提醒"
	message := "您名下存在风险项（" + kind + "，编号 " + strconv.FormatInt(in.TargetID, 10) + "），请尽快处理。"
	if note != "" {
		message += " 处理建议：" + note
	}
	results := a.notifyTenantChannels(c.Request.Context(), tenantID, subject, message)
	outcome := riskNotifyOutcome(results)
	auditOutcome := "failed"
	if outcome != "failed" {
		auditOutcome = "success"
	}
	if e := a.store.recordAudit(a.store.db, sc, meta, "risk:notify", "tenants", tenantID, auditOutcome); e != nil {
		fail(c, 500, "风控通知留痕失败")
		return
	}
	ok(c, gin.H{"kind": kind, "action": "notify", "tenant_id": tenantID, "target_id": in.TargetID, "status": outcome, "channels": results})
}

// riskNotifyOutcome 汇总逐渠道投递结果：全部成功 success，部分成功 partial，否则 failed。
func riskNotifyOutcome(results []gin.H) string {
	if len(results) == 0 {
		return "failed"
	}
	sent := 0
	for _, item := range results {
		if item["status"] == "sent" {
			sent++
		}
	}
	switch {
	case sent == len(results):
		return "success"
	case sent > 0:
		return "partial"
	default:
		return "failed"
	}
}

// riskTenantID 依据风险类型解析所属租户，避免前端误传导致跨租户处置。
func (a *App) riskTenantID(kind string, targetID, declared int64) (int64, error) {
	var tenantID int64
	switch kind {
	case "overdue_bill":
		a.store.db.Table("bills").Select("tenant_id").Where("id=? AND deleted_at IS NULL", targetID).Scan(&tenantID)
	case "pending_order":
		a.store.db.Table("orders").Select("tenant_id").Where("id=? AND deleted_at IS NULL", targetID).Scan(&tenantID)
	case "low_balance":
		a.store.db.Table("accounts").Select("tenant_id").Where("id=? AND deleted_at IS NULL", targetID).Scan(&tenantID)
	case "reclaim_asset":
		a.store.db.Table("assets").Select("tenant_id").Where("id=? AND deleted_at IS NULL", targetID).Scan(&tenantID)
	}
	if tenantID <= 0 {
		return 0, errors.New("风险对象不存在或已处理")
	}
	if declared > 0 && declared != tenantID {
		return 0, errors.New("风险对象与声明的租户不一致")
	}
	return tenantID, nil
}

// notifyTenantChannels 复用告警渠道投递能力，逐渠道返回真实发送结果（最多 5 个渠道）。
func (a *App) notifyTenantChannels(ctx context.Context, tenantID int64, subject, message string) []gin.H {
	var rows []struct {
		notificationChannel
	}
	if err := a.store.db.WithContext(ctx).Table("alert_channels").
		Select("id,tenant_id,project_id,kind,config,secret_ciphertext,secret_key_id,enabled").
		Where("tenant_id=? AND enabled AND deleted_at IS NULL", tenantID).Order("id").Limit(5).Scan(&rows).Error; err != nil {
		return nil
	}
	var profile struct {
		ContactEmail string `gorm:"column:contact_email"`
	}
	a.store.db.Table("tenant_profiles").Select("contact_email").Where("tenant_id=? AND deleted_at IS NULL", tenantID).Take(&profile)
	results := make([]gin.H, 0, len(rows))
	for _, row := range rows {
		channel := row.notificationChannel
		config := channel.Config
		if channel.Kind == "email" {
			var cfg channelConfig
			_ = json.Unmarshal(config, &cfg)
			if strings.TrimSpace(cfg.To) == "" {
				cfg.To = strings.TrimSpace(profile.ContactEmail)
			}
			if cfg.To == "" {
				results = append(results, gin.H{"channel_id": channel.ID, "kind": channel.Kind, "status": "failed", "reason": "邮件渠道未配置收件人且租户无联系邮箱"})
				continue
			}
			updated, err := json.Marshal(cfg)
			if err != nil {
				results = append(results, gin.H{"channel_id": channel.ID, "kind": channel.Kind, "status": "failed", "reason": "邮件渠道配置无效"})
				continue
			}
			config = updated
		}
		channel.Config = config
		// sendChannel 自行解密渠道凭据并校验收发地址，此处不接触明文。
		if err := sendChannel(ctx, channel, subject, message); err != nil {
			results = append(results, gin.H{"channel_id": channel.ID, "kind": channel.Kind, "status": "failed", "reason": "渠道投递失败"})
			continue
		}
		results = append(results, gin.H{"channel_id": channel.ID, "kind": channel.Kind, "status": "sent"})
	}
	return results
}
