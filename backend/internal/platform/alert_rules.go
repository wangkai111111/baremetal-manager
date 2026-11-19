package platform

import (
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type alertRuleValues struct {
	Name            string
	Metric          string
	Operator        string
	Threshold       *float64
	DurationSeconds int
	Severity        string
	Enabled         bool
	Selector        json.RawMessage
}

func (r alertRuleValues) validate() error {
	if strings.TrimSpace(r.Name) == "" || len(r.Name) > 255 {
		return errors.New("请填写规则名称，最多 255 字节")
	}
	if r.Metric != "bmc.health" && r.Metric != "bmc.collection_failed" {
		return errors.New("请选择设备健康或 BMC 连续采集失败指标")
	}
	if !validSeverity(r.Severity) || r.DurationSeconds != 0 {
		return errors.New("告警等级无效；内置监控在采集完成时判定，持续时间需为 0")
	}
	if r.Metric == "bmc.collection_failed" && r.Operator != "collection_failed" {
		return errors.New("采集失败指标请选择连续失败条件，内置监控在连续失败 2 次时告警")
	}
	if r.Operator == "collection_failed" {
		if r.Metric != "bmc.collection_failed" || r.Threshold != nil {
			return errors.New("连续采集失败条件只能用于 BMC 采集失败指标，且不填写阈值")
		}
	} else {
		if !strings.Contains(" gt gte lt lte eq ne ", " "+r.Operator+" ") || r.Threshold == nil || math.IsNaN(*r.Threshold) || math.IsInf(*r.Threshold, 0) || *r.Threshold != math.Trunc(*r.Threshold) {
			return errors.New("请选择比较条件并填写整数阈值")
		}
		if r.Metric == "bmc.health" && (*r.Threshold < 0 || *r.Threshold > 2) {
			return errors.New("健康阈值只能是正常（0）、警告（1）或严重（2）")
		}
	}
	var selector map[string]json.RawMessage
	if json.Unmarshal(r.Selector, &selector) != nil || selector == nil || len(selector) > 1 {
		return errors.New("设备范围只能按服务器标签选择")
	}
	if len(selector) == 1 {
		var tags []string
		if json.Unmarshal(selector["tags"], &tags) != nil || tags == nil || len(tags) > 100 {
			return errors.New("请选择有效的服务器标签，最多 100 个")
		}
		seen := map[string]bool{}
		for _, tag := range tags {
			if strings.TrimSpace(tag) == "" || len(tag) > 255 || seen[tag] {
				return errors.New("服务器标签不能为空或重复")
			}
			seen[tag] = true
		}
	}
	return nil
}

// Save the rule and its channel selection in one transaction so a failed
// association cannot leave a partially created or partially updated rule.
func (a *App) writeAlertRule(create bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		var in struct {
			Name            *string         `json:"name"`
			Metric          *string         `json:"metric"`
			Operator        *string         `json:"operator"`
			Threshold       json.RawMessage `json:"threshold"`
			DurationSeconds *int            `json:"duration_seconds"`
			Severity        *string         `json:"severity"`
			Enabled         *bool           `json:"enabled"`
			Selector        json.RawMessage `json:"selector"`
			ChannelIDs      *[]int64        `json:"channel_ids"`
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 32<<10)
		decoder := json.NewDecoder(c.Request.Body)
		decoder.DisallowUnknownFields()
		if decoder.Decode(&in) != nil || decoder.Decode(new(any)) != io.EOF {
			fail(c, 400, "规则格式无效，请检查所选指标和条件")
			return
		}
		sc, id := contextScope(c), number(c.Param("id"))
		validation := false
		err := a.store.db.WithContext(c.Request.Context()).Transaction(func(tx *gorm.DB) error {
			if err := lockScope(tx, sc); err != nil {
				return err
			}
			rule := alertRuleValues{Severity: "warning", Enabled: true, Selector: json.RawMessage(`{}`)}
			if !create {
				if err := queryScope(tx, resource{table: "alert_rules"}, sc).Clauses(clause.Locking{Strength: "UPDATE"}).Where("id=?", id).Take(&rule).Error; err != nil {
					return err
				}
			}
			if in.Name != nil {
				rule.Name = strings.TrimSpace(*in.Name)
			}
			if in.Metric != nil {
				rule.Metric = *in.Metric
			}
			if in.Operator != nil {
				rule.Operator = *in.Operator
			}
			if in.Threshold != nil {
				if err := json.Unmarshal(in.Threshold, &rule.Threshold); err != nil {
					validation = true
					return errors.New("阈值必须是数字或留空")
				}
			}
			if in.DurationSeconds != nil {
				rule.DurationSeconds = *in.DurationSeconds
			}
			if in.Severity != nil {
				rule.Severity = *in.Severity
			}
			if in.Enabled != nil {
				rule.Enabled = *in.Enabled
			}
			if in.Selector != nil {
				rule.Selector = in.Selector
			}
			if err := rule.validate(); err != nil {
				validation = true
				return err
			}
			if in.ChannelIDs != nil {
				if len(*in.ChannelIDs) > 200 {
					validation = true
					return errors.New("通知渠道最多选择 200 个")
				}
				seen := map[int64]bool{}
				for _, channel := range *in.ChannelIDs {
					if channel <= 0 || seen[channel] {
						validation = true
						return errors.New("通知渠道无效或重复")
					}
					seen[channel] = true
				}
				var found []int64
				if len(*in.ChannelIDs) > 0 {
					if err := queryScope(tx, resource{table: "alert_channels"}, sc).Clauses(clause.Locking{Strength: "UPDATE"}).Where("id IN ?", *in.ChannelIDs).Order("id").Pluck("id", &found).Error; err != nil {
						return err
					}
				}
				if len(found) != len(*in.ChannelIDs) {
					validation = true
					return errors.New("部分通知渠道已删除或不属于当前项目，请重新选择")
				}
			}
			values := map[string]any{"name": rule.Name, "metric": rule.Metric, "operator": rule.Operator, "threshold": rule.Threshold, "duration_seconds": rule.DurationSeconds, "severity": rule.Severity, "enabled": rule.Enabled, "selector": gorm.Expr("?::jsonb", string(rule.Selector))}
			if err := (&store{tx}).saveResource(c.Request.Context(), resource{table: "alert_rules"}, sc, id, values, create, &id); err != nil {
				return err
			}
			if in.ChannelIDs != nil {
				links := queryScope(tx, resource{table: "alert_rule_channels"}, sc).Where("rule_id=?", id)
				if len(*in.ChannelIDs) > 0 {
					links = links.Where("channel_id NOT IN ?", *in.ChannelIDs)
				}
				if err := links.Updates(map[string]any{"deleted_at": time.Now(), "update_by": sc.User}).Error; err != nil {
					return err
				}
				for _, channel := range *in.ChannelIDs {
					if err := tx.Exec(`INSERT INTO alert_rule_channels(tenant_id,project_id,rule_id,channel_id,create_by,update_by) VALUES (?,?,?,?,?,?) ON CONFLICT (tenant_id,project_id,rule_id,channel_id) WHERE deleted_at IS NULL DO NOTHING`, sc.Tenant, sc.Project, id, channel, sc.User, sc.User).Error; err != nil {
						return err
					}
				}
			}
			return nil
		})
		if err != nil {
			if validation {
				fail(c, 400, err.Error())
			} else if errors.Is(err, gorm.ErrRecordNotFound) {
				fail(c, 404, "规则不存在或已删除")
			} else {
				fail(c, 409, "规则保存失败，请检查名称是否重复，或刷新后重试")
			}
			return
		}
		ok(c, gin.H{"id": id})
	}
}

func (a *App) deleteAlertRule(c *gin.Context) {
	sc, id := contextScope(c), number(c.Param("id"))
	err := a.store.db.WithContext(c.Request.Context()).Transaction(func(tx *gorm.DB) error {
		if err := lockScope(tx, sc); err != nil {
			return err
		}
		var rule struct{ ID int64 }
		if err := queryScope(tx, resource{table: "alert_rules"}, sc).Clauses(clause.Locking{Strength: "UPDATE"}).Where("id=?", id).Take(&rule).Error; err != nil {
			return err
		}
		changes := map[string]any{"deleted_at": time.Now(), "update_by": sc.User}
		if err := queryScope(tx, resource{table: "alert_rule_channels"}, sc).Where("rule_id=?", id).Updates(changes).Error; err != nil {
			return err
		}
		if err := queryScope(tx, resource{table: "alert_rules"}, sc).Where("id=?", id).Updates(changes).Error; err != nil {
			return err
		}
		return a.store.audit(tx, sc, "delete", "alert_rules", id)
	})
	if err != nil {
		fail(c, 409, "规则不存在或暂时无法删除，请刷新后重试")
		return
	}
	ok(c, nil)
}
