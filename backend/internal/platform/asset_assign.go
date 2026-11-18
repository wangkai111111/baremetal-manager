package platform

import (
	"encoding/json"
	"errors"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

var errDepartmentMissing = errors.New("部门不存在或已删除")
var errDepartmentChildren = errors.New("请先删除或迁移子部门")
var errDepartmentAssets = errors.New("请先移除该部门下的资产分配（包括其他项目）")
var errDepartmentCycle = errors.New("不能将部门移动到自己的下级部门")

// Membership is organization-wide. Serialize deletion and assignment across
// projects, always before acquiring project locks.
func lockDepartments(tx *gorm.DB, sc scope) error {
	return tx.Exec("SELECT pg_advisory_xact_lock(?, ?)", int32(69421), int32(sc.Tenant)).Error
}
func validateDepartment(tx *gorm.DB, sc scope, id int64) error {
	if id == 0 {
		return nil
	}
	var count int64
	if err := tx.Table("departments").Where("id=? AND tenant_id=? AND deleted_at IS NULL", id, sc.Tenant).Count(&count).Error; err != nil {
		return err
	}
	if count != 1 {
		return errDepartmentMissing
	}
	return nil
}

// Missing must not silently clear assignments; null and zero explicitly clear.
func departmentTarget(raw json.RawMessage) (int64, error) {
	if len(raw) == 0 {
		return 0, errors.New("请指定部门，解除分配请传 null")
	}
	var id *int64
	if err := json.Unmarshal(raw, &id); err != nil {
		return 0, errors.New("无效的部门ID")
	}
	if id == nil {
		return 0, nil
	}
	if *id < 0 {
		return 0, errors.New("无效的部门ID")
	}
	return *id, nil
}

func (s *store) assignAssets(tx *gorm.DB, sc scope, ids []int64, departmentID int64, action string) error {
	if err := lockDepartments(tx, sc); err != nil {
		return err
	}
	if err := lockScope(tx, sc); err != nil {
		return err
	}
	if err := validateDepartment(tx, sc, departmentID); err != nil {
		return err
	}
	if !(&store{tx}).managesDepartment(sc, departmentID) {
		return errors.New("无权分配到该部门")
	}
	q := func() *gorm.DB {
		return queryScope(tx, resource{table: "assets"}, sc).Where("id IN ?", ids).Where(departmentAccessSQL(sc, "assets.department_id", true))
	}
	var count int64
	if err := q().Count(&count).Error; err != nil {
		return err
	}
	if count != int64(len(ids)) {
		return gorm.ErrRecordNotFound
	}
	var value any
	if departmentID > 0 {
		value = departmentID
	}
	res := q().Updates(map[string]any{"department_id": value, "update_by": sc.User})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected != int64(len(ids)) {
		return gorm.ErrRecordNotFound
	}
	for _, id := range ids {
		if err := s.audit(tx, sc, action, "assets", id); err != nil {
			return err
		}
	}
	return nil
}
func (a *App) assignAssetToDepartment(c *gin.Context) {
	id := number(c.Param("id"))
	if id <= 0 {
		fail(c, 400, "无效的资产ID")
		return
	}
	a.assignDepartment(c, []int64{id}, false)
}
func (a *App) batchAssignAssets(c *gin.Context) { a.assignDepartment(c, nil, true) }
func (a *App) assignDepartment(c *gin.Context, ids []int64, batch bool) {
	var in struct {
		AssetIDs     []int64         `json:"asset_ids"`
		DepartmentID json.RawMessage `json:"department_id"`
	}
	if c.ShouldBindJSON(&in) != nil {
		fail(c, 400, "无效的请求体")
		return
	}
	target, err := departmentTarget(in.DepartmentID)
	if err != nil {
		fail(c, 400, err.Error())
		return
	}
	if batch {
		ids = in.AssetIDs
	}
	if len(ids) == 0 || len(ids) > 500 {
		fail(c, 400, "单次分配需选择 1–500 个资产")
		return
	}
	seen := map[int64]bool{}
	unique := make([]int64, 0, len(ids))
	for _, id := range ids {
		if id <= 0 {
			fail(c, 400, "无效的资产ID")
			return
		}
		if !seen[id] {
			seen[id] = true
			unique = append(unique, id)
		}
	}
	action := "assign"
	if batch {
		action = "batch_assign"
	}
	sc := contextScope(c)
	err = a.store.db.WithContext(c.Request.Context()).Transaction(func(tx *gorm.DB) error {
		return a.store.assignAssets(tx, sc, unique, target, action)
	})
	if errors.Is(err, errDepartmentMissing) {
		fail(c, 404, err.Error())
		return
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		fail(c, 404, "部分资产不存在或不属于当前项目")
		return
	}
	if err != nil {
		fail(c, 409, "资产分配失败：无权管理所选范围或存在关联冲突")
		return
	}
	var value any
	if target > 0 {
		value = target
	}
	result := gin.H{"department_id": value}
	if batch {
		result["updated_count"] = len(unique)
	} else {
		result["asset_id"] = ids[0]
	}
	ok(c, result)
}
