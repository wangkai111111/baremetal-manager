package platform

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/minio/minio-go/v7"
	"gorm.io/gorm"
)

const ubuntuCatalogID = "ubuntu-22.04.5-amd64"
const ubuntuFilename = "ubuntu-22.04.5-live-server-amd64.iso"
const ubuntuBaseURL = "https://releases.ubuntu.com/22.04.5/"
const ubuntuTunaBaseURL = "https://mirrors.tuna.tsinghua.edu.cn/ubuntu-releases/22.04.5/"

const ubuntuSHA256 = "9bc6028870aef3f74f4e16b900008179e78b130e6b0b9a140635434a46aa98b0"
const imageMaxBytes int64 = 20 << 30
const checksumMaxBytes int64 = 1 << 20

func imageSourceBaseURL(source string) (string, error) {
	switch source {
	case "", "official":
		return ubuntuBaseURL, nil
	case "tuna":
		return ubuntuTunaBaseURL, nil
	default:
		return "", errors.New("invalid image source")
	}
}

type imageDownloadJob struct {
	ID, TenantID, ProjectID, UserID              int64
	CatalogID, Source, Status, Error, ClaimToken string
	DownloadedBytes, TotalBytes                  int64
	ImageID                                      *int64
}

type catalogImageSource struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	URL  string `json:"url"`
}

type catalogImage struct {
	Source          string               `json:"source"`
	Sources         []catalogImageSource `json:"sources"`
	ID              string               `json:"id"`
	Name            string               `json:"name"`
	OSType          string               `json:"os_type"`
	Version         string               `json:"version"`
	Architecture    string               `json:"architecture"`
	Filename        string               `json:"filename"`
	SourceURL       string               `json:"source_url"`
	Status          string               `json:"status"`
	DownloadedBytes int64                `json:"downloaded_bytes"`
	TotalBytes      int64                `json:"total_bytes"`
	Error           string               `json:"error"`
	ImageID         *int64               `json:"image_id"`
}

func catalogEntry(job imageDownloadJob) catalogImage {
	status := job.Status
	if status == "" {
		status = "available"
	}
	source := job.Source
	if source == "" {
		source = "official"
	}
	baseURL, _ := imageSourceBaseURL(source)
	return catalogImage{
		ID: ubuntuCatalogID, Name: "Ubuntu Server 22.04.5 LTS", OSType: "ubuntu", Version: "22.04.5", Architecture: "x86_64", Filename: ubuntuFilename,
		Source: source, SourceURL: baseURL + ubuntuFilename,
		Sources: []catalogImageSource{
			{ID: "official", Name: "Ubuntu 官方", URL: ubuntuBaseURL + ubuntuFilename},
			{ID: "tuna", Name: "清华镜像站", URL: ubuntuTunaBaseURL + ubuntuFilename},
		},
		Status: status, DownloadedBytes: job.DownloadedBytes, TotalBytes: job.TotalBytes, Error: job.Error, ImageID: job.ImageID,
	}
}

func (a *App) imageCatalog(c *gin.Context) {
	sc := contextScope(c)
	var job imageDownloadJob
	e := a.store.db.WithContext(c.Request.Context()).Table("image_download_jobs").Select(`image_download_jobs.id, tenant_id, project_id, user_id, catalog_id, source, downloaded_bytes, total_bytes, image_id, claim_token,
 CASE WHEN status='ready' AND NOT EXISTS (SELECT 1 FROM images WHERE images.id=image_download_jobs.image_id AND images.tenant_id=image_download_jobs.tenant_id AND images.project_id=image_download_jobs.project_id AND images.deleted_at IS NULL AND images.status='ready') THEN 'failed' ELSE status END AS status,
 CASE WHEN status='ready' AND NOT EXISTS (SELECT 1 FROM images WHERE images.id=image_download_jobs.image_id AND images.tenant_id=image_download_jobs.tenant_id AND images.project_id=image_download_jobs.project_id AND images.deleted_at IS NULL AND images.status='ready') THEN 'downloaded image no longer available; retry required' ELSE error END AS error`).Where("tenant_id=? AND project_id=? AND catalog_id=?", sc.Tenant, sc.Project, ubuntuCatalogID).Take(&job).Error
	if e != nil && !errors.Is(e, gorm.ErrRecordNotFound) {
		fail(c, 500, "image catalog query failed")
		return
	}
	if job.Status == "failed" {
		job.ImageID = nil
	}
	ok(c, []catalogImage{catalogEntry(job)})
}

func (a *App) queueCatalogImage(c *gin.Context) {
	if c.Param("catalogId") != ubuntuCatalogID {
		fail(c, 404, "catalog image not found")
		return
	}
	source := "official"
	if c.Request.Body != nil {
		var in *struct {
			Source json.RawMessage `json:"source"`
		}
		decoder := json.NewDecoder(c.Request.Body)
		decoder.DisallowUnknownFields()
		err := decoder.Decode(&in)
		if err != nil && err != io.EOF {
			fail(c, 400, "invalid image source request")
			return
		}
		if err == nil {
			if in == nil || decoder.Decode(new(any)) != io.EOF {
				fail(c, 400, "invalid image source request")
				return
			}
			if len(in.Source) != 0 {
				source = ""
				if json.Unmarshal(in.Source, &source) != nil || (source != "official" && source != "tuna") {
					fail(c, 400, "source must be official or tuna")
					return
				}
			}
		}
	}
	sc := contextScope(c)
	var job imageDownloadJob
	e := a.store.db.WithContext(c.Request.Context()).Transaction(func(tx *gorm.DB) error {
		// 项目内唯一目录键串行化并发请求及失败重试。
		if e := tx.Exec(`INSERT INTO image_download_jobs (tenant_id,project_id,user_id,catalog_id,source,status) VALUES (?,?,?,?,?,'queued')
   ON CONFLICT (tenant_id,project_id,catalog_id) DO UPDATE SET status='queued',source=EXCLUDED.source,user_id=EXCLUDED.user_id,downloaded_bytes=0,total_bytes=0,error='',image_id=NULL,claim_token=NULL,locked_until=NULL,updated_at=now()
   WHERE image_download_jobs.status='failed' OR (image_download_jobs.status='ready' AND NOT EXISTS (
    SELECT 1 FROM images WHERE images.id=image_download_jobs.image_id AND images.tenant_id=image_download_jobs.tenant_id AND images.project_id=image_download_jobs.project_id AND images.deleted_at IS NULL AND images.status='ready'))`, sc.Tenant, sc.Project, sc.User, ubuntuCatalogID, source).Error; e != nil {
			return e
		}
		return tx.Table("image_download_jobs").Where("tenant_id=? AND project_id=? AND catalog_id=?", sc.Tenant, sc.Project, ubuntuCatalogID).Take(&job).Error
	})
	if e != nil {
		fail(c, 500, "image download queue failed")
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"code": 0, "message": "success", "data": catalogEntry(job)})
}

func trustedImageRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 5 {
		return errors.New("too many redirects")
	}
	host := "releases.ubuntu.com"
	if len(via) > 0 {
		origin := via[0].URL
		if origin.Scheme != "https" || origin.User != nil || (origin.Host != "releases.ubuntu.com" && origin.Host != "mirrors.tuna.tsinghua.edu.cn") {
			return errors.New("untrusted image redirect")
		}
		host = origin.Host
	}
	u := req.URL
	if u.Scheme != "https" || u.Host != host || u.User != nil {
		return errors.New("untrusted image redirect")
	}
	return nil
}

func imageHTTPClient() *http.Client {
	return &http.Client{Timeout: 6 * time.Hour, CheckRedirect: trustedImageRedirect, Transport: &http.Transport{Proxy: http.ProxyFromEnvironment, TLSHandshakeTimeout: 15 * time.Second, ResponseHeaderTimeout: 30 * time.Second, IdleConnTimeout: 90 * time.Second}}
}

func imageGET(ctx context.Context, client *http.Client, address string) (*http.Response, error) {
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if e != nil {
		return nil, e
	}
	req.Header.Set("Accept-Encoding", "identity")
	resp, e := client.Do(req)
	if e != nil {
		return nil, e
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("official source returned HTTP %d", resp.StatusCode)
	}
	return resp, nil
}

func parseImageChecksum(r io.Reader, filename string) (string, error) {
	body, e := io.ReadAll(io.LimitReader(r, checksumMaxBytes+1))
	if e != nil {
		return "", e
	}
	if int64(len(body)) > checksumMaxBytes {
		return "", errors.New("checksum manifest too large")
	}
	scanner := bufio.NewScanner(strings.NewReader(string(body)))
	result := ""
	for scanner.Scan() {
		line := scanner.Text()
		if len(line) < 67 || line[64] != ' ' || (line[65] != ' ' && line[65] != '*') {
			return "", errors.New("invalid SHA256SUMS format")
		}
		sum := line[:64]
		decoded, e := hex.DecodeString(sum)
		if e != nil || len(decoded) != sha256.Size {
			return "", errors.New("invalid SHA256SUMS hash")
		}
		if line[66:] == filename {
			if result != "" {
				return "", errors.New("duplicate checksum entry")
			}
			result = strings.ToLower(sum)
		}
	}
	if e := scanner.Err(); e != nil {
		return "", e
	}
	if result == "" {
		return "", errors.New("official checksum not found")
	}
	return result, nil
}

func streamImage(dst io.Writer, src io.Reader, expected string, maxBytes int64, progress func(int64) error) (int64, error) {
	h := sha256.New()
	buf := make([]byte, 256*1024)
	var size int64
	for {
		n, e := src.Read(buf)
		if n > 0 {
			if int64(n) > maxBytes-size {
				return size, errors.New("image exceeds size limit")
			}
			if _, err := io.MultiWriter(dst, h).Write(buf[:n]); err != nil {
				return size, err
			}
			size += int64(n)
			if progress != nil {
				if err := progress(size); err != nil {
					return size, err
				}
			}
		}
		if e == io.EOF {
			break
		}
		if e != nil {
			return size, e
		}
	}
	if size == 0 {
		return size, errors.New("empty image")
	}
	if hex.EncodeToString(h.Sum(nil)) != expected {
		return size, errors.New("image SHA256 mismatch")
	}
	return size, nil
}

func (a *App) pollImageDownload(ctx context.Context) {
	db := a.store.db.WithContext(ctx)
	// 过期租约标记中断任务；进程启动不能抢占其他工作进程的有效租约。
	if e := db.Exec(`UPDATE image_download_jobs SET status='failed',error='download worker interrupted; retry required',claim_token=NULL,locked_until=NULL,updated_at=now() WHERE status IN ('downloading','verifying') AND locked_until < now()`).Error; e != nil {
		return
	}
	var job imageDownloadJob
	token := uuid.NewString()
	e := db.Raw(`UPDATE image_download_jobs SET status='downloading',claim_token=?,locked_until=now()+interval '2 minutes',updated_at=now()
 WHERE id=(SELECT id FROM image_download_jobs WHERE status='queued' ORDER BY id FOR UPDATE SKIP LOCKED LIMIT 1) RETURNING *`, token).Scan(&job).Error
	if e != nil || job.ID == 0 {
		return
	}
	a.runImageDownload(ctx, job)
}

func (a *App) updateImageJob(ctx context.Context, job imageDownloadJob, values map[string]any) error {
	values["updated_at"] = time.Now()
	if _, exists := values["locked_until"]; !exists {
		values["locked_until"] = time.Now().Add(2 * time.Minute)
	}
	res := a.store.db.WithContext(ctx).Table("image_download_jobs").Where("id=? AND claim_token=? AND status IN ('downloading','verifying') AND locked_until>now()", job.ID, job.ClaimToken).Updates(values)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected != 1 {
		return errors.New("image download lease lost")
	}
	return nil
}

func (a *App) runImageDownload(parent context.Context, job imageDownloadJob) {
	ctx, cancel := context.WithTimeout(parent, 6*time.Hour)
	defer cancel()
	done := make(chan struct{})
	defer close(done)
	go func() {
		t := time.NewTicker(20 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-t.C:
				if a.updateImageJob(ctx, job, map[string]any{}) != nil {
					cancel()
					return
				}
			}
		}
	}()
	e := a.fetchAndStoreImage(ctx, job)
	if e != nil {
		log.Printf("镜像下载失败 job=%d source=%s: %v", job.ID, job.Source, e)
		cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = a.updateImageJob(cleanup, job, map[string]any{"status": "failed", "error": imageDownloadError(e), "claim_token": nil, "locked_until": nil})
	}
}

func imageDownloadError(e error) string {
	// 网络及对象存储错误可能包含内部地址，对外仅返回安全摘要。
	var u *url.Error
	if errors.As(e, &u) {
		return "official source request failed or timed out"
	}
	if errors.Is(e, context.Canceled) || errors.Is(e, context.DeadlineExceeded) {
		return "download interrupted or timed out; retry required"
	}
	switch e.Error() {
	case "official source returned HTTP 403":
		return "镜像源拒绝下载（HTTP 403），请切换镜像来源后重试"
	case "official source returned HTTP 404":
		return "镜像源文件不存在（HTTP 404），请切换镜像来源后重试"
	case "official checksum differs from trusted SHA256", "image SHA256 mismatch", "image exceeds size limit", "empty image", "checksum manifest too large", "official checksum not found", "invalid SHA256SUMS format", "invalid SHA256SUMS hash", "duplicate checksum entry":
		return e.Error()
	}
	return "image download, verification or storage failed; retry required"
}

func (a *App) fetchAndStoreImage(ctx context.Context, job imageDownloadJob) error {
	baseURL, e := imageSourceBaseURL(job.Source)
	if e != nil {
		return e
	}
	client := imageHTTPClient()
	defer client.CloseIdleConnections()
	sums, e := imageGET(ctx, client, baseURL+"SHA256SUMS")
	if e != nil {
		return e
	}
	expected, e := parseImageChecksum(sums.Body, ubuntuFilename)
	sums.Body.Close()
	if e != nil {
		return e
	}
	if expected != ubuntuSHA256 {
		return errors.New("official checksum differs from trusted SHA256")
	}
	resp, e := imageGET(ctx, client, baseURL+ubuntuFilename)
	if e != nil {
		return e
	}
	defer resp.Body.Close()
	if resp.ContentLength > imageMaxBytes {
		return errors.New("image exceeds size limit")
	}
	total := resp.ContentLength
	if total < 0 {
		total = 0
	}
	if e = a.updateImageJob(ctx, job, map[string]any{"total_bytes": total}); e != nil {
		return e
	}
	f, e := os.CreateTemp("", "baremetal-image-*.iso")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	defer f.Close()
	last := time.Time{}
	size, e := streamImage(f, resp.Body, expected, imageMaxBytes, func(n int64) error {
		if time.Since(last) < time.Second {
			return nil
		}
		last = time.Now()
		return a.updateImageJob(ctx, job, map[string]any{"downloaded_bytes": n})
	})
	if e != nil {
		return e
	}
	if resp.ContentLength >= 0 && size != resp.ContentLength {
		return errors.New("image length mismatch")
	}
	if e = a.updateImageJob(ctx, job, map[string]any{"status": "verifying", "downloaded_bytes": size, "total_bytes": size}); e != nil {
		return e
	}
	storage, bucket, e := objectStorage()
	if e != nil || bucket == "" {
		return errors.New("object storage unavailable")
	}
	if _, e = f.Seek(0, io.SeekStart); e != nil {
		return e
	}
	key := fmt.Sprintf("%d/%d/%s", job.TenantID, job.ProjectID, uuid.NewString())
	if _, e = storage.PutObject(ctx, bucket, key, f, size, minio.PutObjectOptions{ContentType: "application/octet-stream"}); e != nil {
		return errors.New("object storage upload failed")
	}
	// 原子提交镜像元数据和就绪状态，并阻止失效租约提交。
	e = a.store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var owned imageDownloadJob
		if e := tx.Raw("SELECT * FROM image_download_jobs WHERE id=? AND claim_token=? AND status='verifying' AND locked_until>now() FOR UPDATE", job.ID, job.ClaimToken).Scan(&owned).Error; e != nil {
			return e
		}
		if owned.ID == 0 {
			return errors.New("image download lease lost")
		}
		var imageID int64
		s := &store{db: tx}
		in := map[string]any{"name": ubuntuFilename, "object_key": key, "size_bytes": size, "sha256": expected, "os_type": "ubuntu", "version": "22.04.5", "architecture": "x86_64", "status": "ready"}
		if e := s.saveResource(ctx, resource{table: "images"}, scope{Tenant: job.TenantID, Project: job.ProjectID, User: job.UserID}, 0, in, true, &imageID); e != nil {
			return e
		}
		return tx.Table("image_download_jobs").Where("id=? AND claim_token=?", job.ID, job.ClaimToken).Updates(map[string]any{"status": "ready", "image_id": imageID, "error": "", "claim_token": nil, "locked_until": nil, "updated_at": time.Now()}).Error
	})
	if e != nil {
		cleanup, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = storage.RemoveObject(cleanup, bucket, key, minio.RemoveObjectOptions{})
	}
	return e
}
