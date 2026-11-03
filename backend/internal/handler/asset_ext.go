package handler

import (
	"encoding/json"
	"fmt"
	"net"
	"time"

	"baremetal-manager/backend/internal/adapter"
	"baremetal-manager/backend/internal/model"
	"baremetal-manager/backend/internal/repository"
	"baremetal-manager/backend/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/skip2/go-qrcode"
	"github.com/xuri/excelize/v2"
	"gorm.io/gorm"
)

// RegisterAssetExtRoutes 注册资产管理增强路由
func RegisterAssetExtRoutes(rg *gin.RouterGroup, store *repository.Store, bmcAdapter *adapter.BMC) {
	h := &Handler{S: &service.Service{Store: store}}

	// Excel 批量导入
	rg.POST("/assets/import", h.importAssets)

	// 网段扫描发现（异步任务）
	rg.POST("/assets/scan-range", h.scanRange)

	// 资产二维码
	rg.GET("/assets/:id/qrcode", h.getAssetQRCode)

	// 跨项目迁移
	rg.POST("/assets/:id/migrate", h.migrateAsset)
}

// importAssets Excel 批量导入资产
// 解析上传的 Excel 文件，逐行验证并创建资产记录，单行失败不影响其他行
func (h *Handler) importAssets(c *gin.Context) {
	file, err := c.FormFile("file")
	if err != nil {
		Fail(c, 400, "INVALID_FILE", "请上传 Excel 文件")
		return
	}

	// 检查文件扩展名
	if ext := file.Filename; ext == "" || (len(ext) < 5 || (ext[len(ext)-5:] != ".xlsx" && ext[len(ext)-4:] != ".xls")) {
		Fail(c, 400, "INVALID_FORMAT", "仅支持 .xlsx 格式的 Excel 文件")
		return
	}

	f, err := file.Open()
	if err != nil {
		Fail(c, 400, "INVALID_FILE", "无法打开上传的文件")
		return
	}
	defer f.Close()

	xl, err := excelize.OpenReader(f)
	if err != nil {
		Fail(c, 400, "PARSE_ERROR", "无法解析 Excel 文件")
		return
	}
	defer xl.Close()

	sheet := xl.GetSheetName(xl.GetActiveSheetIndex())
	rows, err := xl.GetRows(sheet)
	if err != nil || len(rows) < 2 {
		Fail(c, 400, "EMPTY_FILE", "Excel 文件为空或缺少数据行")
		return
	}

	// 解析表头，建立列名到索引的映射
	header := rows[0]
	colIndex := make(map[string]int)
	for i, name := range header {
		colIndex[name] = i
	}

	// 验证必填列是否存在
	if _, ok := colIndex["资产名称"]; !ok {
		Fail(c, 400, "MISSING_COLUMN", "缺少必填列：资产名称")
		return
	}
	if _, ok := colIndex["BMC地址"]; !ok {
		Fail(c, 400, "MISSING_COLUMN", "缺少必填列：BMC地址")
		return
	}

	projectID := pid(c)
	successCount := 0
	failCount := 0
	var failDetails []map[string]interface{}

	// 逐行解析并创建资产
	for rowIdx := 1; rowIdx < len(rows); rowIdx++ {
		row := rows[rowIdx]
		getVal := func(col string) string {
			if idx, ok := colIndex[col]; ok && idx < len(row) {
				return row[idx]
			}
			return ""
		}

		name := getVal("资产名称")
		bmcAddr := getVal("BMC地址")

		// 验证必填字段
		if name == "" || bmcAddr == "" {
			failCount++
			failDetails = append(failDetails, map[string]interface{}{
				"row":   rowIdx + 1,
				"error": "资产名称和BMC地址为必填字段",
			})
			continue
		}

		// 检查同名资产是否已存在
		var existing int64
		h.S.Store.DB.Model(&model.Asset{}).Where("project_id = ? AND name = ?", projectID, name).Count(&existing)
		if existing > 0 {
			failCount++
			failDetails = append(failDetails, map[string]interface{}{
				"row":   rowIdx + 1,
				"name":  name,
				"error": "同名资产已存在",
			})
			continue
		}

		// 构造资产记录
		asset := model.Asset{
			ProjectID:    projectID,
			Name:         name,
			BMCAddress:   bmcAddr,
			BMCUser:      getVal("BMC用户名"),
			IPAddress:    getVal("业务IP"),
			MACAddress:   getVal("MAC地址"),
			SerialNumber: getVal("序列号"),
			Manufacturer: getVal("制造商"),
			Model:        getVal("型号"),
		}

		// 加密 BMC 密码
		secret := getVal("BMC密码")
		if secret != "" {
			encrypted, err := service.EncryptCredential(secret)
			if err != nil {
				failCount++
				failDetails = append(failDetails, map[string]interface{}{
					"row":   rowIdx + 1,
					"name":  name,
					"error": "BMC密码加密失败: " + err.Error(),
				})
				continue
			}
			asset.BMCSecret = encrypted
		}

		if err := h.S.Store.DB.Create(&asset).Error; err != nil {
			failCount++
			failDetails = append(failDetails, map[string]interface{}{
				"row":   rowIdx + 1,
				"name":  name,
				"error": "创建失败: " + err.Error(),
			})
			continue
		}

		successCount++
	}

	// 记录审计日志
	h.S.Store.Audit(uid(c), projectID, "asset.import", fmt.Sprintf("成功%d条，失败%d条", successCount, failCount))

	OK(c, map[string]interface{}{
		"success_count": successCount,
		"fail_count":    failCount,
		"fail_details":  failDetails,
	})
}

// scanRange 网段扫描发现（异步任务）
// 解析 CIDR 网段，提交异步扫描任务，通过 Redfish 探测网络设备
func (h *Handler) scanRange(c *gin.Context) {
	var req struct {
		CIDR      string `json:"cidr"`
		StartPort int    `json:"start_port"`
		EndPort   int    `json:"end_port"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.CIDR == "" {
		Fail(c, 400, "INVALID_REQUEST", "请提供 CIDR 网段参数")
		return
	}

	// 验证 CIDR 格式
	_, _, err := net.ParseCIDR(req.CIDR)
	if err != nil {
		Fail(c, 400, "INVALID_CIDR", "无效的 CIDR 网段格式")
		return
	}

	// 设置默认端口范围
	if req.StartPort == 0 {
		req.StartPort = 443
	}
	if req.EndPort == 0 {
		req.EndPort = 443
	}
	if req.StartPort < 1 || req.EndPort > 65535 || req.StartPort > req.EndPort {
		Fail(c, 400, "INVALID_PORT", "端口范围无效")
		return
	}

	projectID := pid(c)
	userID := uid(c)

	// 构造扫描任务载荷
	scanPayload, _ := json.Marshal(map[string]interface{}{
		"action":      "scan_range",
		"cidr":        req.CIDR,
		"start_port":  req.StartPort,
		"end_port":    req.EndPort,
	})

	// 创建异步操作记录，状态为 queued
	op := &model.Operation{
		UserID:    userID,
		ProjectID: projectID,
		Kind:      "inspect",
		Payload:   string(scanPayload),
		Status:    "queued",
	}
	if err := h.S.Store.DB.Create(op).Error; err != nil {
		Fail(c, 500, "CREATE_FAILED", "创建扫描任务失败")
		return
	}

	// 记录审计日志
	h.S.Store.Audit(userID, projectID, "asset.scan_range", req.CIDR)

	c.JSON(202, Response{Code: 0, Data: op, Message: "扫描任务已提交"})
}

// getAssetQRCode 生成资产二维码
// 二维码内容为资产详情页 URL，返回 PNG 图片
func (h *Handler) getAssetQRCode(c *gin.Context) {
	assetID := id(c)
	projectID := pid(c)

	var asset model.Asset
	if err := h.S.Store.DB.Where("project_id = ?", projectID).First(&asset, assetID).Error; err != nil {
		Fail(c, 404, "NOT_FOUND", "资产不存在")
		return
	}

	// 生成二维码内容 URL
	host := c.Request.Host
	content := fmt.Sprintf("https://%s/assets/%d", host, assetID)

	// 生成 PNG 图片（256x256）
	png, err := qrcode.Encode(content, qrcode.Medium, 256)
	if err != nil {
		Fail(c, 500, "QRCODE_ERROR", "生成二维码失败")
		return
	}

	c.Header("Content-Disposition", fmt.Sprintf("inline; filename=\"asset-%d-qrcode.png\"", assetID))
	c.Data(200, "image/png", png)
}

// migrateAsset 跨项目迁移资产
// 将资产及其关联的标签、告警、操作记录、审计记录迁移到目标项目
func (h *Handler) migrateAsset(c *gin.Context) {
	assetID := id(c)
	projectID := pid(c)

	var req struct {
		TargetProjectID uint `json:"target_project_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.TargetProjectID == 0 {
		Fail(c, 400, "INVALID_REQUEST", "请提供目标项目 ID")
		return
	}

	// 不允许迁移到当前项目
	if req.TargetProjectID == projectID {
		Fail(c, 400, "SAME_PROJECT", "目标项目与当前项目相同")
		return
	}

	// 验证源资产存在
	var asset model.Asset
	if err := h.S.Store.DB.Where("project_id = ? AND id = ?", projectID, assetID).First(&asset).Error; err != nil {
		Fail(c, 404, "NOT_FOUND", "资产不存在")
		return
	}

	// 验证目标项目存在
	var targetProject model.Project
	if err := h.S.Store.DB.First(&targetProject, req.TargetProjectID).Error; err != nil {
		Fail(c, 404, "TARGET_NOT_FOUND", "目标项目不存在")
		return
	}

	// 在事务中执行迁移，确保数据一致性
	err := h.S.Store.DB.Transaction(func(tx *gorm.DB) error {
		// 更新资产的项目归属
		if e := tx.Model(&asset).Update("project_id", req.TargetProjectID).Error; e != nil {
			return e
		}

		// 迁移关联的标签（删除旧关联，标签定义保留在原项目）
		if e := tx.Where("asset_id = ?", assetID).Delete(&model.AssetTag{}).Error; e != nil {
			return e
		}

		// 迁移关联的告警记录
		if e := tx.Model(&model.Alert{}).Where("asset_id = ?", assetID).
			Update("project_id", req.TargetProjectID).Error; e != nil {
			return e
		}

		// 迁移关联的操作记录
		if e := tx.Model(&model.Operation{}).Where("asset_id = ?", assetID).
			Update("project_id", req.TargetProjectID).Error; e != nil {
			return e
		}

		// 迁移关联的审计记录
		if e := tx.Model(&model.Audit{}).Where("target = ?", fmt.Sprintf("%d", assetID)).
			Where("project_id = ?", projectID).
			Update("project_id", req.TargetProjectID).Error; e != nil {
			return e
		}

		return nil
	})
	if err != nil {
		Fail(c, 500, "MIGRATE_FAILED", "迁移失败: "+err.Error())
		return
	}

	// 在源项目记录迁出审计日志
	h.S.Store.Audit(uid(c), projectID, "asset.migrate_out",
		fmt.Sprintf("资产 %d 迁移至项目 %d", assetID, req.TargetProjectID))
	// 在目标项目记录迁入审计日志
	h.S.Store.Audit(uid(c), req.TargetProjectID, "asset.migrate_in",
		fmt.Sprintf("资产 %d 从项目 %d 迁入", assetID, projectID))

	OK(c, map[string]interface{}{
		"asset_id":        assetID,
		"target_project_id": req.TargetProjectID,
		"migrated_at":     time.Now(),
	})
}
