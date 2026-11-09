package platform

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"gorm.io/gorm"
)

func objectStorage() (*minio.Client, string, error) {
	endpoint := os.Getenv("MINIO_ENDPOINT")
	client, e := minio.New(strings.TrimPrefix(strings.TrimPrefix(endpoint, "https://"), "http://"), &minio.Options{Creds: credentials.NewStaticV4(os.Getenv("MINIO_ACCESS_KEY"), os.Getenv("MINIO_SECRET_KEY"), ""), Secure: strings.HasPrefix(endpoint, "https://")})
	return client, os.Getenv("MINIO_BUCKET"), e
}
func (a *App) createImage(c *gin.Context) {
	if !strings.HasPrefix(c.ContentType(), "multipart/") {
		fail(c, 400, "multipart image upload required")
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 20<<30)
	file, e := c.FormFile("file")
	if e != nil || file.Size <= 0 || file.Size > 20<<30 {
		fail(c, 400, "invalid image file")
		return
	}
	if c.Request.MultipartForm != nil {
		defer c.Request.MultipartForm.RemoveAll()
	}
	f, e := file.Open()
	if e != nil {
		fail(c, 400, "invalid image file")
		return
	}
	defer f.Close()
	osType, version, arch := c.PostForm("os_type"), c.PostForm("version"), c.PostForm("architecture")
	if osType == "" || version == "" || (arch != "x86_64" && arch != "aarch64") {
		fail(c, 400, "os_type, version and supported architecture required")
		return
	}
	client, bucket, e := objectStorage()
	if e != nil || bucket == "" {
		fail(c, 503, "object storage unavailable")
		return
	}
	sc := contextScope(c)
	key := fmt.Sprintf("%d/%d/%s", sc.Tenant, sc.Project, uuid.NewString())
	hash := sha256.New()
	_, e = client.PutObject(c.Request.Context(), bucket, key, io.TeeReader(f, hash), file.Size, minio.PutObjectOptions{ContentType: "application/octet-stream"})
	if e != nil {
		fail(c, 502, "image upload failed")
		return
	}
	in := map[string]any{"name": file.Filename, "object_key": key, "size_bytes": file.Size, "sha256": hex.EncodeToString(hash.Sum(nil)), "os_type": osType, "version": version, "architecture": arch, "status": "ready"}
	var id int64
	e = a.store.saveResource(c.Request.Context(), resource{table: "images"}, sc, 0, in, true, &id)
	if e != nil {
		_ = client.RemoveObject(c.Request.Context(), bucket, key, minio.RemoveObjectOptions{})
		fail(c, 409, "image metadata persistence failed")
		return
	}
	ok(c, gin.H{"id": id, "sha256": in["sha256"], "status": "ready"})
}
func (a *App) downloadImage(c *gin.Context) {
	var image struct {
		Name, ObjectKey string
		SizeBytes       int64
	}
	if e := queryScope(a.store.db, resource{table: "images"}, contextScope(c)).Where("id=? AND status='ready'", number(c.Param("id"))).Take(&image).Error; e != nil {
		fail(c, 404, "image not found")
		return
	}
	client, bucket, e := objectStorage()
	if e != nil {
		fail(c, 503, "object storage unavailable")
		return
	}
	obj, e := client.GetObject(c.Request.Context(), bucket, image.ObjectKey, minio.GetObjectOptions{})
	if e != nil {
		fail(c, 502, "object storage unavailable")
		return
	}
	defer obj.Close()
	if _, e = obj.Stat(); e != nil {
		fail(c, 502, "image object unavailable")
		return
	}
	c.Header("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": image.Name}))
	c.DataFromReader(200, image.SizeBytes, "application/octet-stream", obj, nil)
}
func (a *App) openAPI(c *gin.Context) {
	paths := map[string]any{}
	for _, route := range a.router.Routes() {
		if !strings.HasPrefix(route.Path, "/api/v1/") {
			continue
		}
		parts := strings.Split(route.Path, "/")
		for i, s := range parts {
			if strings.HasPrefix(s, ":") {
				parts[i] = "{" + s[1:] + "}"
			}
		}
		path := strings.Join(parts, "/")
		item, exists := paths[path]
		if !exists {
			item = map[string]any{}
			paths[path] = item
		}
		item.(map[string]any)[strings.ToLower(route.Method)] = gin.H{"responses": gin.H{"200": gin.H{"description": "Response envelope or binary image / WebSocket for documented endpoints"}}}
	}
	c.JSON(200, gin.H{"openapi": "3.0.3", "info": gin.H{"title": "Baremetal target platform API", "version": "1.0.0", "description": "Only actual registered routes are listed. Device installation and firmware execution are unavailable (501)."}, "paths": paths})
}
func (a *App) alertTransition(c *gin.Context) {
	sc := contextScope(c)
	status := c.Param("transition")
	if status != "resolve" && status != "acknowledge" {
		fail(c, 400, "invalid alert transition")
		return
	}
	e := a.store.db.Transaction(func(tx *gorm.DB) error {
		updates := map[string]any{"status": "resolved", "resolved_at": gorm.Expr("now()"), "resolved_by": sc.User}
		event := "resolved"
		if status == "acknowledge" {
			updates = map[string]any{"status": "acknowledged", "acknowledged_at": gorm.Expr("now()"), "acknowledged_by": sc.User}
			event = "acknowledged"
		}
		res := queryScope(tx, resource{table: "alerts"}, sc).Where("id=? AND status<>'resolved'", number(c.Param("id"))).Updates(updates)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected != 1 {
			return gorm.ErrRecordNotFound
		}
		return tx.Table("alert_events").Create(map[string]any{"tenant_id": sc.Tenant, "project_id": sc.Project, "alert_id": number(c.Param("id")), "event_type": event, "message": "alert state updated by authorized user"}).Error
	})
	if e != nil {
		fail(c, 409, "alert transition conflict")
		return
	}
	ok(c, nil)
}
