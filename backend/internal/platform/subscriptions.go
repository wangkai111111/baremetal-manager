package platform

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type subscriptionTerm struct {
	ID                       int64
	StartsAt, EndsAt         time.Time
	PaidCents, RefundedCents int64
}

func termRefund(t subscriptionTerm, now time.Time) int64 {
	start := t.StartsAt
	if start.Before(now) {
		start = now
	}
	if !t.EndsAt.After(start) || !t.EndsAt.After(t.StartsAt) || t.PaidCents <= 0 {
		return 0
	}
	// Integer arithmetic rounds down to cents without floating point over-refunds.
	amount := new(big.Int).Mul(big.NewInt(t.PaidCents), big.NewInt(int64(t.EndsAt.Sub(start))))
	amount.Quo(amount, big.NewInt(int64(t.EndsAt.Sub(t.StartsAt))))
	refund := amount.Int64() - t.RefundedCents
	if refund < 0 {
		return 0
	}
	return refund
}

func verifiedTerms(tx *gorm.DB, sc scope, assetID int64) ([]subscriptionTerm, error) {
	var terms []subscriptionTerm
	err := tx.Table("subscription_terms AS t").Select("t.*").
		Joins("JOIN orders o ON o.id=t.order_id AND o.asset_id=t.asset_id").
		Joins("JOIN subscription_payments p ON p.id=t.payment_id AND p.order_id=o.id AND p.paid_cents=t.paid_cents").
		Joins("JOIN accounts a ON a.id=p.account_id AND a.tenant_id=o.tenant_id AND a.deleted_at IS NULL").
		Joins("JOIN payments actual ON actual.id=p.payment_id AND actual.id=o.payment_id AND actual.account_id=a.id AND actual.status='success' AND actual.paid_at IS NOT NULL AND actual.deleted_at IS NULL AND actual.amount*100=p.paid_cents AND actual.paid_at=p.paid_at AND actual.paid_at=o.paid_at").
		Where("o.term_starts_at=t.starts_at AND o.term_ends_at=t.ends_at").
		Where("t.asset_id=? AND o.tenant_id=? AND o.project_id=? AND o.status='completed' AND o.type IN ('purchase','renew') AND o.paid_at IS NOT NULL AND o.payable_cents=t.paid_cents AND o.deleted_at IS NULL", assetID, sc.Tenant, sc.Project).
		Order("t.starts_at").Clauses(clause.Locking{Strength: "UPDATE", Table: clause.Table{Name: "t"}}).Find(&terms).Error
	if err != nil {
		return nil, err
	}
	var recorded int64
	if err = tx.Table("subscription_terms").Where("asset_id=?", assetID).Count(&recorded).Error; err != nil {
		return nil, err
	}
	if recorded != int64(len(terms)) {
		return nil, errors.New("存在无法核验支付依据的租期，需人工核验")
	}
	if len(terms) == 0 {
		return nil, errors.New("缺少可核验的已支付租期，拒绝自动退款")
	}
	for i, t := range terms {
		if !t.EndsAt.After(t.StartsAt) || t.PaidCents <= 0 || t.RefundedCents < 0 || t.RefundedCents > t.PaidCents || (i > 0 && t.StartsAt.Before(terms[i-1].EndsAt)) {
			return nil, errors.New("租期数据无效或重叠，需人工核验")
		}
	}
	return terms, nil
}

func (a *App) subscriptionPreview(c *gin.Context) {
	sc := contextScope(c)
	id := number(c.Param("id"))
	var result gin.H
	err := a.store.db.Transaction(func(tx *gorm.DB) error {
		var asset struct {
			ID                int64
			Name              string
			LeaseEnd          *time.Time
			SubscriptionState string
		}
		if err := queryScope(tx, resource{table: "assets"}, sc).Where("tenant_id=? AND project_id=? AND id=?", sc.Tenant, sc.Project, id).Take(&asset).Error; err != nil {
			return errors.New("资产不存在或无权访问")
		}
		if asset.SubscriptionState == "reclaim_pending" {
			return errors.New("资产已进入待人工回收状态")
		}
		terms, err := verifiedTerms(tx, sc, id)
		if err != nil {
			return err
		}
		if asset.LeaseEnd == nil || !asset.LeaseEnd.Equal(terms[len(terms)-1].EndsAt) {
			return errors.New("缺少可靠到期时间或租期记录不一致，需人工核验")
		}
		now := time.Now()
		var total int64
		for _, t := range terms {
			total += termRefund(t, now)
		}
		result = gin.H{"asset_id": id, "asset_name": asset.Name, "refund_cents": total, "power_action": "power_off", "reclaim": "待人工回收，不清盘"}
		return nil
	})
	if err != nil {
		fail(c, 422, err.Error())
		return
	}
	ok(c, result)
}

func subscriptionKey(c *gin.Context, key string) string {
	if key == "" {
		key = c.GetHeader("Idempotency-Key")
	}
	return strings.TrimSpace(key)
}
func validSubscriptionBatch(ids []int64, key string) bool {
	if len(ids) == 0 || len(ids) > 100 || key == "" || len(key) > 80 {
		return false
	}
	seen := map[int64]bool{}
	for _, id := range ids {
		if id <= 0 || seen[id] {
			return false
		}
		seen[id] = true
	}
	return true
}

func replaySubscription(tx *gorm.DB, sc scope, key, fingerprint string) (gin.H, bool, error) {
	var old struct {
		Fingerprint string
		Response    json.RawMessage
	}
	err := tx.Table("subscription_requests").Where("tenant_id=? AND project_id=? AND request_key=?", sc.Tenant, sc.Project, key).Take(&old).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if old.Fingerprint != fingerprint {
		return nil, false, errors.New("幂等请求键已用于其他操作")
	}
	var result gin.H
	if err = json.Unmarshal(old.Response, &result); err != nil {
		return nil, false, err
	}
	result["replayed"] = true
	return result, true, nil
}
func saveSubscriptionResponse(tx *gorm.DB, sc scope, key, fingerprint string, result gin.H) error {
	b, err := json.Marshal(result)
	if err != nil {
		return err
	}
	return tx.Table("subscription_requests").Create(map[string]any{"tenant_id": sc.Tenant, "project_id": sc.Project, "request_key": key, "fingerprint": fingerprint, "response": string(b)}).Error
}

func (a *App) unsubscribeAsset(c *gin.Context) {
	if !a.subscriptionFinanceAllowed(c) {
		return
	}
	var in struct {
		RequestKey string `json:"request_key"`
	}
	if c.GetHeader("Idempotency-Key") == "" && c.ShouldBindJSON(&in) != nil {
		fail(c, 400, "无效请求")
		return
	}
	key := subscriptionKey(c, in.RequestKey)
	id := number(c.Param("id"))
	if !validSubscriptionBatch([]int64{id}, key) {
		fail(c, 400, "必须提供有效资产和幂等请求键（最多80字符）")
		return
	}
	result, err := a.unsubscribeOne(c, contextScope(c), id, key)
	if err != nil {
		fail(c, 409, err.Error())
		return
	}
	ok(c, result)
}
func (a *App) subscriptionFinanceAllowed(c *gin.Context) bool {
	sc := contextScope(c)
	if !a.store.allowed(sc, "orders:write") || !a.store.allowed(sc, "finance:write") {
		fail(c, 403, "需要 orders:write 和 finance:write 权限")
		return false
	}
	return true
}

func (a *App) batchUnsubscribeSubscriptions(c *gin.Context) {
	if !a.subscriptionFinanceAllowed(c) {
		return
	}
	var in struct {
		AssetIDs   []int64 `json:"asset_ids"`
		RequestKey string  `json:"request_key"`
	}
	if c.ShouldBindJSON(&in) != nil {
		fail(c, 400, "无效请求")
		return
	}
	key := subscriptionKey(c, in.RequestKey)
	if !validSubscriptionBatch(in.AssetIDs, key) {
		fail(c, 400, "请选择1至100台不同资产并提供幂等请求键（最多80字符）")
		return
	}
	results := make([]gin.H, 0, len(in.AssetIDs))
	sc := contextScope(c)
	for _, id := range in.AssetIDs {
		result, err := a.unsubscribeOne(c, sc, id, fmt.Sprintf("%s:%d", key, id))
		if err != nil {
			result = gin.H{"asset_id": id, "status": "failed", "reason": err.Error()}
		}
		results = append(results, result)
	}
	ok(c, gin.H{"results": results})
}
func (a *App) unsubscribeOne(c *gin.Context, sc scope, id int64, key string) (gin.H, error) {
	var result gin.H
	err := a.store.db.WithContext(c.Request.Context()).Transaction(func(tx *gorm.DB) error {
		if err := lockScope(tx, sc); err != nil {
			return err
		}
		fingerprint := fmt.Sprintf("unsubscribe:%d:%d", sc.User, id)
		old, replayed, err := replaySubscription(tx, sc, key, fingerprint)
		if err != nil {
			return err
		}
		if replayed {
			result = old
			return nil
		}
		var asset struct {
			ID                int64
			SubscriptionState string
			LeaseEnd          *time.Time
		}
		if err := queryScope(tx, resource{table: "assets"}, sc).Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND project_id=? AND id=?", sc.Tenant, sc.Project, id).Take(&asset).Error; err != nil {
			return errors.New("资产不存在或无权访问")
		}
		if asset.SubscriptionState == "reclaim_pending" {
			return errors.New("资产已退订，待人工回收，不得重复退款")
		}
		terms, err := verifiedTerms(tx, sc, id)
		if err != nil {
			return err
		}
		if asset.LeaseEnd == nil || !asset.LeaseEnd.Equal(terms[len(terms)-1].EndsAt) {
			return errors.New("缺少可靠到期时间或租期记录不一致，需人工核验")
		}
		// Do not race a running BMC/install action; pending operations can be cancelled safely.
		var active int64
		if err = tx.Table("operations").Where("tenant_id=? AND project_id=? AND asset_id=? AND status='running' AND deleted_at IS NULL", sc.Tenant, sc.Project, id).Count(&active).Error; err != nil {
			return err
		}
		if active > 0 {
			return errors.New("资产有正在执行的任务，请稍后退订")
		}
		var account struct{ ID int64 }
		if err = tx.Table("accounts").Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND deleted_at IS NULL", sc.Tenant).Take(&account).Error; err != nil {
			return errors.New("租户余额账户不存在，无法退款")
		}
		now := time.Now()
		var total int64
		for _, t := range terms {
			amount := termRefund(t, now)
			if amount == 0 {
				continue
			}
			total += amount
			if err = tx.Table("subscription_terms").Where("id=?", t.ID).Update("refunded_cents", gorm.Expr("refunded_cents+?", amount)).Error; err != nil {
				return err
			}
			if err = tx.Table("subscription_refunds").Create(map[string]any{"term_id": t.ID, "account_id": account.ID, "amount_cents": amount, "request_key": fmt.Sprintf("%s:%d", key, t.ID)}).Error; err != nil {
				return err
			}
		}
		if total > 0 {
			if err = tx.Table("accounts").Where("id=?", account.ID).Update("balance", gorm.Expr("balance+?::numeric/100", total)).Error; err != nil {
				return err
			}
		}
		if err = tx.Table("operations").Where("tenant_id=? AND project_id=? AND asset_id=? AND status IN ('pending','retry_wait') AND deleted_at IS NULL", sc.Tenant, sc.Project, id).Update("status", "cancelled").Error; err != nil {
			return err
		}
		if err = tx.Table("assets").Where("id=?", id).Updates(map[string]any{"subscription_state": "reclaim_pending", "unsubscribed_at": now}).Error; err != nil {
			return err
		}
		if err = tx.Table("asset_monitor_configs").Where("tenant_id=? AND project_id=? AND asset_id=? AND deleted_at IS NULL", sc.Tenant, sc.Project, id).Update("enabled", false).Error; err != nil {
			return err
		}
		row := map[string]any{"tenant_id": sc.Tenant, "project_id": sc.Project, "asset_id": id, "user_id": sc.User, "kind": "power_off", "idempotency_key": key + ":off", "status": "pending", "max_attempts": 1, "timeout_seconds": 60}
		if err = tx.Table("operations").Clauses(clause.Returning{Columns: []clause.Column{{Name: "id"}}}).Create(&row).Error; err != nil {
			return err
		}
		opID := asInt(row["id"])
		if err = tx.Table("task_outbox").Create(map[string]any{"tenant_id": sc.Tenant, "project_id": sc.Project, "operation_id": opID, "event_key": key + ":off", "event_type": "operation.execute"}).Error; err != nil {
			return err
		}
		result = gin.H{"asset_id": id, "refund_cents": total, "status": "reclaim_pending", "power_operation_id": opID, "message": "退款已入租户余额，关机任务已提交，已撤销客户使用权限；待人工回收，不清盘"}
		if err = a.store.audit(tx, sc, "unsubscribe", "assets", id); err != nil {
			return err
		}
		return saveSubscriptionResponse(tx, sc, key, fingerprint, result)
	})
	return result, err
}

func (a *App) paySubscriptionOrder(c *gin.Context) {
	sc := contextScope(c)
	if !a.store.allowed(sc, "orders:write") || !a.store.allowed(sc, "finance:write") {
		fail(c, 403, "需要 orders:write 和 finance:write 权限")
		return
	}
	id := number(c.Param("id"))
	var result gin.H
	err := a.store.db.WithContext(c.Request.Context()).Transaction(func(tx *gorm.DB) error {
		if err := lockScope(tx, sc); err != nil {
			return err
		}
		var order struct {
			ID, AssetID, PayableCents int64
			Status, Type, TermCycle   string
			TermCount                 int
			PaymentID                 *int64
		}
		if err := tx.Table("orders").Clauses(clause.Locking{Strength: "UPDATE"}).Where("id=? AND tenant_id=? AND project_id=? AND deleted_at IS NULL", id, sc.Tenant, sc.Project).Take(&order).Error; err != nil {
			return errors.New("订阅订单不存在或无权访问")
		}
		if order.Type != "renew" || order.PayableCents <= 0 {
			return errors.New("仅支持服务端生成的订阅续费订单")
		}
		if order.Status == "completed" {
			terms, err := verifiedTerms(tx, sc, order.AssetID)
			if err != nil {
				return err
			}
			var found int64
			if err = tx.Table("subscription_terms").Where("order_id=?", id).Count(&found).Error; err != nil {
				return err
			}
			if found != 1 || len(terms) == 0 {
				return errors.New("订单缺少可核验租期")
			}
			result = gin.H{"order_id": id, "paid_cents": order.PayableCents, "status": "completed", "replayed": true, "message": "订单已支付，未重复扣款"}
			return nil
		}
		if order.Status != "pending" || order.PaymentID != nil {
			return errors.New("订单当前状态不可支付")
		}
		var asset struct {
			ID                int64
			LeaseEnd          *time.Time
			SubscriptionState string
		}
		if err := queryScope(tx, resource{table: "assets"}, sc).Clauses(clause.Locking{Strength: "UPDATE"}).Where("id=? AND status<>'retired'", order.AssetID).Take(&asset).Error; err != nil {
			return errors.New("资产不存在或无权使用")
		}
		if asset.SubscriptionState == "reclaim_pending" || asset.LeaseEnd == nil {
			return errors.New("资产已退订或缺少可靠到期时间，拒绝支付")
		}
		var recorded int64
		if err := tx.Table("subscription_terms").Where("asset_id=?", asset.ID).Count(&recorded).Error; err != nil {
			return err
		}
		if recorded > 0 {
			terms, err := verifiedTerms(tx, sc, asset.ID)
			if err != nil {
				return err
			}
			if !asset.LeaseEnd.Equal(terms[len(terms)-1].EndsAt) {
				return errors.New("资产到期时间与已付租期不一致，拒绝支付")
			}
		}
		now := time.Now().UTC().Truncate(time.Microsecond)
		start := *asset.LeaseEnd
		if start.Before(now) {
			start = now
		}
		end, err := subscriptionEnd(start, order.TermCycle, order.TermCount)
		if err != nil {
			return err
		}
		var account struct{ ID int64 }
		if err = tx.Table("accounts").Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND deleted_at IS NULL", sc.Tenant).Take(&account).Error; err != nil {
			return errors.New("余额账户不存在")
		}
		debit := tx.Table("accounts").Where("id=? AND balance-frozen_amount>=?::numeric/100", account.ID, order.PayableCents).Update("balance", gorm.Expr("balance-?::numeric/100", order.PayableCents))
		if debit.Error != nil {
			return debit.Error
		}
		if debit.RowsAffected != 1 {
			return errors.New("可用余额不足")
		}
		payment := map[string]any{"account_id": account.ID, "amount": gorm.Expr("?::numeric/100", order.PayableCents), "method": "balance", "status": "success", "paid_at": now, "transaction_id": fmt.Sprintf("subscription-order:%d", id)}
		if err = tx.Table("payments").Clauses(clause.Returning{Columns: []clause.Column{{Name: "id"}}}).Create(&payment).Error; err != nil {
			return err
		}
		paymentID := asInt(payment["id"])
		receipt := map[string]any{"order_id": id, "account_id": account.ID, "payment_id": paymentID, "paid_cents": order.PayableCents, "paid_at": now}
		if err = tx.Table("subscription_payments").Clauses(clause.Returning{Columns: []clause.Column{{Name: "id"}}}).Create(&receipt).Error; err != nil {
			return err
		}
		if err = tx.Table("subscription_terms").Create(map[string]any{"asset_id": asset.ID, "order_id": id, "payment_id": asInt(receipt["id"]), "starts_at": start, "ends_at": end, "paid_cents": order.PayableCents}).Error; err != nil {
			return err
		}
		if err = tx.Table("orders").Where("id=?", id).Updates(map[string]any{"status": "completed", "paid_at": now, "completed_at": now, "payment_id": paymentID, "term_starts_at": start, "term_ends_at": end}).Error; err != nil {
			return err
		}
		if err = tx.Table("assets").Where("id=?", asset.ID).Updates(map[string]any{"lease_end": end, "subscription_state": "active"}).Error; err != nil {
			return err
		}
		if err = a.store.audit(tx, sc, "pay:balance", "orders", id); err != nil {
			return err
		}
		result = gin.H{"order_id": id, "asset_id": asset.ID, "status": "completed", "paid_cents": order.PayableCents, "lease_end": end, "payment_id": paymentID, "message": "余额支付成功，租期已生效"}
		return nil
	})
	if err != nil {
		fail(c, 409, err.Error())
		return
	}
	ok(c, result)
}

func subscriptionEnd(start time.Time, cycle string, count int) (time.Time, error) {
	if count < 1 || count > 36 {
		return time.Time{}, errors.New("无效周期数量")
	}
	switch cycle {
	case "hourly":
		return start.Add(time.Duration(count) * time.Hour), nil
	case "daily":
		return start.AddDate(0, 0, count), nil
	case "monthly":
		return start.AddDate(0, count, 0), nil
	case "yearly":
		return start.AddDate(count, 0, 0), nil
	default:
		return time.Time{}, errors.New("无效续费周期")
	}
}

func renewalPrice(pricing string, billingCycle, cycle string, count int) (int64, error) {
	if count < 1 || count > 36 {
		return 0, errors.New("续费数量必须为1至36")
	}
	if cycle != "hourly" && cycle != "daily" && cycle != "monthly" && cycle != "yearly" {
		return 0, errors.New("无效续费周期")
	}
	var prices map[string]float64
	if json.Unmarshal([]byte(pricing), &prices) != nil {
		return 0, errors.New("产品缺少可靠定价")
	}
	price := prices[cycle]
	if price == 0 && billingCycle == cycle {
		price = prices["base_price"]
	}
	if price <= 0 || math.IsNaN(price) || math.IsInf(price, 0) || price > 1e8 {
		return 0, errors.New("产品未配置该周期有效价格")
	}
	cents := int64(math.Round(price*100)) * int64(count)
	if cents <= 0 || cents > 999999999999 {
		return 0, errors.New("续费金额超出范围")
	}
	return cents, nil
}
func (a *App) batchRenewSubscriptions(c *gin.Context) {
	var in struct {
		AssetIDs   []int64 `json:"asset_ids"`
		RequestKey string  `json:"request_key"`
		Cycle      string  `json:"cycle"`
		Count      int     `json:"count"`
	}
	if c.ShouldBindJSON(&in) != nil {
		fail(c, 400, "无效请求")
		return
	}
	key := subscriptionKey(c, in.RequestKey)
	if !validSubscriptionBatch(in.AssetIDs, key) || in.Count < 1 || in.Count > 36 {
		fail(c, 400, "请选择1至100台不同资产、1至36个周期并提供幂等请求键")
		return
	}
	results := make([]gin.H, 0, len(in.AssetIDs))
	sc := contextScope(c)
	for _, id := range in.AssetIDs {
		var result gin.H
		err := a.store.db.WithContext(c.Request.Context()).Transaction(func(tx *gorm.DB) error {
			if err := lockScope(tx, sc); err != nil {
				return err
			}
			requestKey := fmt.Sprintf("%s:%d", key, id)
			fingerprint := fmt.Sprintf("renew:%d:%d:%s:%d", sc.User, id, in.Cycle, in.Count)
			old, replayed, err := replaySubscription(tx, sc, requestKey, fingerprint)
			if err != nil {
				return err
			}
			if replayed {
				result = old
				return nil
			}
			var asset struct {
				ID                int64
				SubscriptionState string
			}
			if err = queryScope(tx, resource{table: "assets"}, sc).Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND project_id=? AND id=? AND status<>'retired'", sc.Tenant, sc.Project, id).Take(&asset).Error; err != nil {
				return errors.New("资产不存在或无权访问")
			}
			if asset.SubscriptionState == "reclaim_pending" {
				return errors.New("资产已退订，不允许续费")
			}
			var purchase struct{ ProductID int64 }
			if err = tx.Table("orders").Select("product_id").Where("asset_id=? AND tenant_id=? AND type='purchase' AND status='completed' AND deleted_at IS NULL", id, sc.Tenant).Order("id DESC").Take(&purchase).Error; err != nil {
				return errors.New("未找到已交付购买订单，无法确定续费产品")
			}
			var product struct {
				ID           int64
				PricingJSON  string
				BillingCycle string
			}
			if err = tx.Table("products").Where("id=? AND tenant_id=? AND status='active' AND deleted_at IS NULL", purchase.ProductID, sc.Tenant).Take(&product).Error; err != nil {
				return errors.New("续费产品不存在或已下架")
			}
			cents, err := renewalPrice(product.PricingJSON, product.BillingCycle, in.Cycle, in.Count)
			if err != nil {
				return err
			}
			var pending int64
			if err = tx.Table("orders").Where("asset_id=? AND tenant_id=? AND type='renew' AND status IN ('pending','paid','processing') AND deleted_at IS NULL", id, sc.Tenant).Count(&pending).Error; err != nil {
				return err
			}
			if pending > 0 {
				return errors.New("已有未完成续费订单，请先处理该订单")
			}
			row := map[string]any{"tenant_id": sc.Tenant, "project_id": sc.Project, "user_id": sc.User, "type": "renew", "asset_id": id, "product_id": product.ID, "amount": float64(cents) / 100, "final_amount": float64(cents) / 100, "status": "pending", "payable_cents": cents, "term_cycle": in.Cycle, "term_count": in.Count}
			if err = tx.Table("orders").Clauses(clause.Returning{Columns: []clause.Column{{Name: "id"}}}).Create(&row).Error; err != nil {
				return err
			}
			orderID := asInt(row["id"])
			result = gin.H{"asset_id": id, "status": "pending", "order_id": orderID, "payable_cents": cents, "message": "已生成待支付续费订单，未扣款且未修改到期时间"}
			if err = a.store.audit(tx, sc, "create:renew", "orders", orderID); err != nil {
				return err
			}
			return saveSubscriptionResponse(tx, sc, requestKey, fingerprint, result)
		})
		if err != nil {
			result = gin.H{"asset_id": id, "status": "failed", "reason": err.Error()}
		}
		results = append(results, result)
	}
	ok(c, gin.H{"results": results})
}
