package platform

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type limitedBody struct {
	io.ReadCloser
	remaining int64
}

func (b *limitedBody) Read(p []byte) (int, error) {
	if b.remaining <= 0 {
		return 0, errors.New("body too large")
	}
	if int64(len(p)) > b.remaining {
		p = p[:b.remaining]
	}
	n, e := b.ReadCloser.Read(p)
	b.remaining -= int64(n)
	return n, e
}
func lockScope(tx *gorm.DB, sc scope) error {
	var n int64
	return tx.Raw("SELECT id FROM projects WHERE tenant_id=? AND id=? AND deleted_at IS NULL AND status='active' FOR UPDATE", sc.Tenant, sc.Project).Scan(&n).Error
}
func (s *store) saveResource(ctx context.Context, r resource, sc scope, id int64, in map[string]any, create bool, out *int64) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if e := lockScope(tx, sc); e != nil {
			return e
		}
		if !create {
			var old map[string]any
			if e := queryScope(tx, r, sc).Where("id=?", id).Take(&old).Error; e != nil {
				return e
			}
			for k, v := range old {
				if _, ok := in[k]; !ok {
					if _, ref := references[k]; ref || k == "rack_u_position" || k == "height_u" || k == "start_address" || k == "end_address" || k == "address" {
						in[k] = v
					}
				}
			}
		}
		for k, parent := range references {
			if v, ok := in[k]; ok && v != nil {
				var row map[string]any
				if e := queryScope(tx, resource{table: parent}, sc).Clauses(clause.Locking{Strength: "UPDATE"}).Where("id=?", v).Take(&row).Error; e != nil {
					return errors.New("relation not found")
				}
			}
		}
		if r.table == "assets" {
			if e := validateRack(tx, sc, id, in); e != nil {
				return e
			}
			var n int64
			tx.Table("device_leases").Where("tenant_id=? AND project_id=? AND asset_id=? AND expires_at>now()", sc.Tenant, sc.Project, id).Count(&n)
			if n > 0 {
				return errors.New("asset busy")
			}
			if create {
				in["bmc_tls_verify"] = true
			}
		}
		if r.table == "ip_pools" || r.table == "ip_allocations" {
			var network struct{ Subnet string }
			if e := queryScope(tx, resource{table: "networks"}, sc).Select("subnet::text").Where("id=?", in["network_id"]).Take(&network).Error; e != nil {
				return e
			}
			prefix, e := netip.ParsePrefix(network.Subnet)
			if e != nil {
				return e
			}
			for _, k := range []string{"address", "start_address", "end_address"} {
				if v, ok := in[k]; ok {
					ip, e := netip.ParseAddr(strings.Split(fmt.Sprint(v), "/")[0])
					if e != nil || !prefix.Contains(ip) {
						return errors.New("address outside network")
					}
				}
			}
			if pool, ok := in["pool_id"]; ok && pool != nil {
				var valid bool
				e = tx.Raw("SELECT EXISTS(SELECT 1 FROM ip_pools WHERE id=? AND tenant_id=? AND project_id=? AND network_id=? AND deleted_at IS NULL AND ?::inet BETWEEN start_address AND end_address)", pool, sc.Tenant, sc.Project, in["network_id"], in["address"]).Scan(&valid).Error
				if e != nil || !valid {
					return errors.New("address outside pool")
				}
			}
		}
		in["update_by"] = sc.User
		if create {
			in["tenant_id"] = sc.Tenant
			in["project_id"] = sc.Project
			in["create_by"] = sc.User
			if e := tx.Table(r.table).Clauses(clause.Returning{Columns: []clause.Column{{Name: "id"}}}).Create(&in).Error; e != nil {
				return e
			}
			id = asInt(in["id"])
		} else {
			res := queryScope(tx, r, sc).Where("id=?", id).Updates(in)
			if res.Error != nil {
				return res.Error
			}
			if res.RowsAffected != 1 {
				return gorm.ErrRecordNotFound
			}
		}
		*out = id
		return s.audit(tx, sc, "write", r.table, id)
	})
}
func asInt(v any) int64 {
	switch x := v.(type) {
	case int64:
		return x
	case int:
		return int64(x)
	case float64:
		return int64(x)
	default:
		return number(fmt.Sprint(v))
	}
}
func validateRack(tx *gorm.DB, sc scope, id int64, in map[string]any) error {
	rack := asInt(in["rack_id"])
	position := asInt(in["rack_u_position"])
	height := asInt(in["height_u"])
	if height == 0 {
		height = 1
	}
	if rack == 0 {
		if position != 0 {
			return errors.New("rack required")
		}
		return nil
	}
	var max int64
	if e := queryScope(tx, resource{table: "racks"}, sc).Select("height_u").Where("id=?", rack).Scan(&max).Error; e != nil {
		return e
	}
	if position < 1 || position+height-1 > max {
		return errors.New("invalid rack position")
	}
	var n int64
	// 机架占位是项目级约束，不能因读取隔离而忽略其他用户的设备。
	rackScope := sc
	rackScope.Admin = true
	queryScope(tx, resource{table: "assets"}, rackScope).Where("rack_id=? AND id<>? AND rack_u_position < ? AND rack_u_position+height_u > ?", rack, id, position+height, position).Count(&n)
	if n > 0 {
		return errors.New("rack position occupied")
	}
	return nil
}
func (s *store) deleteResource(ctx context.Context, r resource, sc scope, id int64) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if e := lockScope(tx, sc); e != nil {
			return e
		}
		if r.table == "assets" {
			var n int64
			tx.Table("device_leases").Where("tenant_id=? AND project_id=? AND asset_id=? AND expires_at>now()", sc.Tenant, sc.Project, id).Count(&n)
			if n > 0 {
				return errors.New("asset busy")
			}
		}
		for field, parent := range references {
			if parent != r.table {
				continue
			}
			for _, child := range resources {
				if !strings.Contains(" "+child.fields+" ", " "+field+" ") {
					continue
				}
				var n int64
				if e := queryScope(tx, child, sc).Where(field+"=?", id).Count(&n).Error; e != nil {
					return e
				}
				if n > 0 {
					return errors.New("resource referenced")
				}
			}
		}
		res := queryScope(tx, r, sc).Where("id=?", id).Updates(map[string]any{"deleted_at": time.Now(), "update_by": sc.User})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected != 1 {
			return gorm.ErrRecordNotFound
		}
		return s.audit(tx, sc, "delete", r.table, id)
	})
}
