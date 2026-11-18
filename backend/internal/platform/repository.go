package platform

import (
	"context"
	"errors"
	"fmt"
	"io"
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
		if r.table == "assets" {
			if err := lockDepartments(tx, sc); err != nil {
				return err
			}
			if !create && !(&store{tx}).managesAsset(sc, id) {
				return errAccessDenied
			}
		}
		if e := lockScope(tx, sc); e != nil {
			return e
		}
		var old map[string]any
		if !create {
			if e := queryScope(tx, r, sc).Where("id=?", id).Take(&old).Error; e != nil {
				return e
			}
			if r.table == "assets" && isTestVMIdentity(fmt.Sprint(old["manufacturer"]), fmt.Sprint(old["model"]), fmt.Sprint(old["mac_address"])) {
				return errTestVMLabRequired
			}
			if r.table == "assets" {
				dateValue := func(key string) *string {
					v, exists := in[key]
					if !exists {
						v = old[key]
					}
					if v == nil {
						return nil
					}
					var value string
					switch d := v.(type) {
					case time.Time:
						value = d.Format("2006-01-02")
					case string:
						if len(d) >= 10 {
							value = d[:10]
						} else {
							value = d
						}
					}
					return &value
				}
				life := lifecycleInput{PurchaseDate: dateValue("purchase_date"), WarrantyExpiry: dateValue("warranty_expiry")}
				if err := life.validate(); err != nil {
					return err
				}
			}
			for k, v := range old {
				if _, ok := in[k]; !ok {
					if _, ref := references[k]; ref || k == "start_address" || k == "end_address" || k == "address" {
						in[k] = v
					}
				}
			}
		}
		if v, exists := in["asset_id"]; exists && v != nil && !(&store{tx}).managesAsset(sc, asInt(v)) {
			return errAccessDenied
		}
		for k, parent := range references {
			if v, ok := in[k]; ok && v != nil {
				if parent == "departments" {
					if err := validateDepartment(tx, sc, asInt(v)); err != nil {
						return err
					}
					continue
				}
				var row map[string]any
				relation := queryScope(tx, resource{table: parent}, sc)
				if e := relation.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id=?", v).Take(&row).Error; e != nil {
					return errors.New("relation not found")
				}
			}
		}
		if r.table == "assets" {
			if !(&store{tx}).managesDepartment(sc, asInt(in["department_id"])) {
				return errors.New("无权分配到该部门；请指定管理范围内的部门")
			}
			var n int64
			tx.Table("device_leases").Where("tenant_id=? AND project_id=? AND asset_id=? AND expires_at>now()", sc.Tenant, sc.Project, id).Count(&n)
			if n > 0 {
				return errors.New("asset busy")
			}
			if create {
				if _, ok := in["bmc_tls_verify"]; !ok {
					in["bmc_tls_verify"] = false
				}
			}
		}
		if err := validateNetworkRelations(tx, r, sc, id, in, old); err != nil {
			return err
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
			if r.table == "assets" && (in["bmc_protocol"] == nil || in["bmc_protocol"] == "redfish") && in["status"] != "retired" {
				if err := enqueueAutomaticCollection(tx, sc, id); err != nil {
					return err
				}
			}
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
func (s *store) deleteResource(ctx context.Context, r resource, sc scope, id int64) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if r.table == "assets" {
			if err := lockDepartments(tx, sc); err != nil {
				return err
			}
			if !(&store{tx}).managesAsset(sc, id) {
				return errAccessDenied
			}
		}
		if e := lockScope(tx, sc); e != nil {
			return e
		}
		if r.table == "assets" {
			var identity device
			if err := queryScope(tx, r, sc).Clauses(clause.Locking{Strength: "UPDATE"}).Where("id=?", id).Take(&identity).Error; err != nil {
				return err
			}
			if isTestVMIdentity(identity.Manufacturer, identity.Model, identity.MACAddress) {
				return errTestVMLabRequired
			}
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
				childScope := sc
				childScope.Admin = true
				if e := queryScope(tx, child, childScope).Where(field+"=?", id).Count(&n).Error; e != nil {
					return e
				}
				if n > 0 {
					return errors.New("resource referenced")
				}
			}
		}
		q := queryScope(tx, r, sc).Where("id=?", id)
		if !sc.Admin && strings.Contains(" "+r.fields+" ", " asset_id ") {
			q = q.Where(assetAccessSQL(sc, r.table+".asset_id", true))
		}
		res := q.Updates(map[string]any{"deleted_at": time.Now(), "update_by": sc.User})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected != 1 {
			return gorm.ErrRecordNotFound
		}
		return s.audit(tx, sc, "delete", r.table, id)
	})
}
