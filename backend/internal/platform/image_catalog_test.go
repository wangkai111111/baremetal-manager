package platform

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql/driver"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestCatalogEntry(t *testing.T) {
	entry := catalogEntry(imageDownloadJob{})
	if entry.Status != "available" || entry.ImageID != nil || entry.SourceURL != ubuntuBaseURL+ubuntuFilename || entry.ID != ubuntuCatalogID {
		t.Fatalf("unexpected entry: %+v", entry)
	}
	body, err := json.Marshal(entry)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(body, &fields); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"id", "name", "os_type", "version", "architecture", "filename", "source", "sources", "source_url", "status", "downloaded_bytes", "total_bytes", "error", "image_id"} {
		if _, ok := fields[name]; !ok {
			t.Errorf("missing field %s", name)
		}
	}
	if len(fields) != 14 {
		t.Fatalf("unexpected fields: %s", body)
	}
	for _, status := range []string{"queued", "downloading", "verifying", "ready", "failed"} {
		if got := catalogEntry(imageDownloadJob{Status: status}).Status; got != status {
			t.Errorf("status = %s", got)
		}
	}
}

func TestImageSources(t *testing.T) {
	for source, want := range map[string]string{"": ubuntuBaseURL, "official": ubuntuBaseURL, "tuna": ubuntuTunaBaseURL} {
		baseURL, err := imageSourceBaseURL(source)
		if err != nil || baseURL != want {
			t.Fatalf("source %q: %q, %v", source, baseURL, err)
		}
		entry := catalogEntry(imageDownloadJob{Source: source})
		if entry.SourceURL != want+ubuntuFilename || entry.Source == "" || len(entry.Sources) != 2 {
			t.Fatalf("source %q: %+v", source, entry)
		}
		for i, expected := range []catalogImageSource{
			{ID: "official", Name: "Ubuntu 官方", URL: ubuntuBaseURL + ubuntuFilename},
			{ID: "tuna", Name: "清华镜像站", URL: ubuntuTunaBaseURL + ubuntuFilename},
		} {
			if entry.Sources[i] != expected {
				t.Fatalf("sources: %+v", entry.Sources)
			}
		}
	}
	for _, source := range []string{"https://evil.example/image.iso", "TUNA", "other"} {
		if _, err := imageSourceBaseURL(source); err == nil {
			t.Fatalf("accepted source %q", source)
		}
		if err := (&App{}).fetchAndStoreImage(context.Background(), imageDownloadJob{Source: source}); err == nil {
			t.Fatalf("worker accepted source %q", source)
		}
	}
}

func TestQueueCatalogImageSourceValidation(t *testing.T) {
	for _, body := range []string{`{"source":""}`, `{"source":null}`, `{"source":7}`, `{"source":true}`, `{"source":"TUNA"}`, `{"source":"https://evil.example/image.iso"}`, `{"url":"https://evil.example/image.iso"}`, `null`, `[]`, `{"source":`, `{} {}`} {
		t.Run(body, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest("POST", "/", strings.NewReader(body))
			c.Params = gin.Params{{Key: "catalogId", Value: ubuntuCatalogID}}
			(&App{}).queueCatalogImage(c)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("status %d: %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestParseImageChecksum(t *testing.T) {
	for _, prefix := range []string{"  ", " *"} {
		sum, err := parseImageChecksum(strings.NewReader(strings.ToUpper(ubuntuSHA256)+prefix+ubuntuFilename+"\n"), ubuntuFilename)
		if err != nil || sum != ubuntuSHA256 {
			t.Fatalf("checksum = %q, %v", sum, err)
		}
	}
	valid := ubuntuSHA256 + " *" + ubuntuFilename + "\n"
	for name, body := range map[string]string{
		"missing": "", "other file": ubuntuSHA256 + " *other.iso\n", "duplicate": valid + valid,
		"bad hash": strings.Repeat("z", 64) + " *" + ubuntuFilename, "bad separator": ubuntuSHA256 + "xx" + ubuntuFilename,
		"oversized": strings.Repeat("x", int(checksumMaxBytes)+1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := parseImageChecksum(strings.NewReader(body), ubuntuFilename); err == nil {
				t.Fatal("invalid manifest accepted")
			}
		})
	}
}

func TestStreamImage(t *testing.T) {
	data := bytes.Repeat([]byte("test ISO content"), 30000)
	sum := sha256.Sum256(data)
	expected := hex.EncodeToString(sum[:])
	var dst bytes.Buffer
	var progress int64
	size, err := streamImage(&dst, bytes.NewReader(data), expected, int64(len(data)), func(n int64) error {
		if n <= progress {
			t.Fatal("non-monotonic progress")
		}
		progress = n
		return nil
	})
	if err != nil || size != int64(len(data)) || progress != size || !bytes.Equal(dst.Bytes(), data) {
		t.Fatalf("stream = %d, %v", size, err)
	}
	for _, tc := range []struct {
		name  string
		data  []byte
		hash  string
		limit int64
		want  string
	}{
		{"mismatch", data, strings.Repeat("0", 64), int64(len(data)), "image SHA256 mismatch"},
		{"oversized", data, expected, 10, "image exceeds size limit"},
		{"empty", nil, expected, 10, "empty image"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := streamImage(io.Discard, bytes.NewReader(tc.data), tc.hash, tc.limit, nil)
			if err == nil || err.Error() != tc.want {
				t.Fatalf("error = %v", err)
			}
		})
	}
	stop := errors.New("lease lost")
	if _, err := streamImage(io.Discard, bytes.NewReader(data), expected, int64(len(data)), func(int64) error { return stop }); !errors.Is(err, stop) {
		t.Fatalf("progress failure = %v", err)
	}
	if _, err := streamImage(failingImageWriter{}, bytes.NewReader(data), expected, int64(len(data)), nil); err == nil {
		t.Fatal("write failure ignored")
	}
}

type failingImageWriter struct{}

func (failingImageWriter) Write([]byte) (int, error) { return 0, errors.New("write failed") }

func TestTrustedImageRedirect(t *testing.T) {
	for _, address := range []string{"http://releases.ubuntu.com/image", "https://evil.example/image", "https://releases.ubuntu.com.evil.example/image", "https://user@releases.ubuntu.com/image", "https://releases.ubuntu.com:444/image"} {
		u, _ := url.Parse(address)
		if trustedImageRedirect(&http.Request{URL: u}, nil) == nil {
			t.Errorf("accepted %s", address)
		}
	}
	u, _ := url.Parse(ubuntuBaseURL + ubuntuFilename)
	if err := trustedImageRedirect(&http.Request{URL: u}, nil); err != nil {
		t.Fatal(err)
	}
	if trustedImageRedirect(&http.Request{URL: u}, make([]*http.Request, 5)) == nil {
		t.Fatal("redirect loop accepted")
	}
}

func TestImageRedirectSameSource(t *testing.T) {
	for _, baseURL := range []string{ubuntuBaseURL, ubuntuTunaBaseURL} {
		origin, _ := http.NewRequest("GET", baseURL+ubuntuFilename, nil)
		for _, address := range []string{
			ubuntuBaseURL + ubuntuFilename, ubuntuTunaBaseURL + ubuntuFilename,
			strings.Replace(baseURL, "https:", "http:", 1) + ubuntuFilename,
			"https://mirrors.tuna.tsinghua.edu.cn.evil.example/image",
			"https://mirrors.tuna.tsinghua.edu.cn:444/image",
			"https://user@mirrors.tuna.tsinghua.edu.cn/image",
			"https://evil.example/image",
		} {
			req, _ := http.NewRequest("GET", address, nil)
			err := trustedImageRedirect(req, []*http.Request{origin})
			wantAllowed := address == baseURL+ubuntuFilename
			if (err == nil) != wantAllowed {
				t.Errorf("redirect %s -> %s: %v", baseURL, address, err)
			}
		}
	}
	origin, _ := http.NewRequest("GET", "https://evil.example/image", nil)
	req, _ := http.NewRequest("GET", ubuntuBaseURL+ubuntuFilename, nil)
	if trustedImageRedirect(req, []*http.Request{origin}) == nil {
		t.Fatal("untrusted origin accepted")
	}
}

func TestImageGET(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Accept-Encoding") != "identity" {
			t.Error("non-identity encoding")
		}
		if r.URL.Path == "/missing" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = io.WriteString(w, "small fixture")
	}))
	defer server.Close()
	resp, err := imageGET(context.Background(), server.Client(), server.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if _, err := imageGET(context.Background(), server.Client(), server.URL+"/missing"); err == nil {
		t.Fatal("HTTP failure accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := imageGET(ctx, server.Client(), server.URL); err == nil {
		t.Fatal("cancel ignored")
	}
}

func TestImageDownloadError(t *testing.T) {
	secret := "https://internal.example/private?token=sensitive"
	for _, err := range []error{errors.New(secret), &url.Error{Op: "Get", URL: secret, Err: errors.New("failed")}} {
		got := imageDownloadError(err)
		if strings.Contains(got, "internal.example") || strings.Contains(got, "sensitive") {
			t.Fatalf("unsafe error: %s", got)
		}
	}
	if got := imageDownloadError(errors.New("official checksum differs from trusted SHA256")); got != "official checksum differs from trusted SHA256" {
		t.Fatal(got)
	}
	if got := imageDownloadError(context.DeadlineExceeded); !strings.Contains(got, "timed out") {
		t.Fatal(got)
	}
}

func TestCatalogRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	a := &App{}
	router := a.routes()
	for _, tc := range []struct{ method, path string }{{"GET", "/api/v1/projects/1/images/catalog"}, {"POST", "/api/v1/projects/1/images/catalog/" + ubuntuCatalogID + "/download"}} {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, nil))
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("%s %s = %d", tc.method, tc.path, w.Code)
		}
	}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "catalogId", Value: "untrusted-url"}}
	a.queueCatalogImage(c)
	if w.Code != http.StatusNotFound {
		t.Fatalf("unknown catalog = %d", w.Code)
	}
}

func TestImageCatalogWithoutJobs(t *testing.T) {
	db, conn := monitoringDB(t, monitorSQLStep{match: "FROM \"image_download_jobs\"", columns: []string{"id", "status"}})
	a := &App{store: &store{db: db}}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/", nil)
	c.Set("scope", scope{Tenant: 11, Project: 22})
	a.imageCatalog(c)
	var body struct {
		Data []catalogImage `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || len(body.Data) != 1 || body.Data[0].Status != "available" || body.Data[0].ImageID != nil {
		t.Fatalf("response: %s", w.Body.String())
	}
	query := conn.queries[0]
	for _, part := range []string{"catalog_id, source,", "images.deleted_at IS NULL", "images.status='ready'", "tenant_id=$1 AND project_id=$2", "THEN 'failed'"} {
		if !strings.Contains(query, part) {
			t.Errorf("missing %q: %s", part, query)
		}
	}
}

func TestQueueCatalogImageSQL(t *testing.T) {
	for _, status := range []string{"queued", "downloading", "verifying", "ready"} {
		t.Run(status, func(t *testing.T) {
			db, conn := monitoringDB(t,
				monitorSQLStep{match: "ON CONFLICT (tenant_id,project_id,catalog_id) DO UPDATE"},
				monitorSQLStep{match: "FROM \"image_download_jobs\"", columns: []string{"id", "status"}, rows: [][]driver.Value{{int64(1), status}}},
			)
			a := &App{store: &store{db: db}}
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest("POST", "/", nil)
			c.Set("scope", scope{Tenant: 11, Project: 22, User: 33})
			c.Params = gin.Params{{Key: "catalogId", Value: ubuntuCatalogID}}
			a.queueCatalogImage(c)
			var body struct {
				Data catalogImage `json:"data"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if w.Code != 202 || body.Data.Status != status {
				t.Fatalf("response: %s", w.Body.String())
			}
			for _, part := range []string{"catalog_id,source,status) VALUES", "source=EXCLUDED.source", "WHERE image_download_jobs.status='failed'", "images.deleted_at IS NULL", "images.tenant_id=image_download_jobs.tenant_id", "images.project_id=image_download_jobs.project_id", "image_id=NULL", "claim_token=NULL"} {
				if !strings.Contains(conn.queries[0], part) {
					t.Errorf("missing %q", part)
				}
			}
		})
	}
}

func TestQueueCatalogImageSources(t *testing.T) {
	for _, tc := range []struct{ body, stored string }{
		{"", "official"}, {`{}`, "official"}, {`{"source":"official"}`, "official"},
		{`{"source":"tuna"}`, "tuna"}, {`{"source":"tuna"}`, "official"}, {`{"source":"official"}`, "tuna"},
	} {
		t.Run(tc.body+"/"+tc.stored, func(t *testing.T) {
			db, _ := monitoringDB(t,
				monitorSQLStep{match: "source=EXCLUDED.source"},
				monitorSQLStep{match: "FROM \"image_download_jobs\"", columns: []string{"id", "status", "source"}, rows: [][]driver.Value{{int64(1), "queued", tc.stored}}},
			)
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest("POST", "/", strings.NewReader(tc.body))
			c.Set("scope", scope{Tenant: 11, Project: 22, User: 33})
			c.Params = gin.Params{{Key: "catalogId", Value: ubuntuCatalogID}}
			(&App{store: &store{db: db}}).queueCatalogImage(c)
			var body struct {
				Data catalogImage `json:"data"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			baseURL, _ := imageSourceBaseURL(tc.stored)
			if w.Code != 202 || body.Data.Source != tc.stored || body.Data.SourceURL != baseURL+ubuntuFilename {
				t.Fatalf("response: %s", w.Body.String())
			}
		})
	}
}

func TestImageJobLeaseAndRecoverySQL(t *testing.T) {
	db, conn := monitoringDB(t,
		monitorSQLStep{match: "UPDATE \"image_download_jobs\""},
		monitorSQLStep{match: "download worker interrupted; retry required"},
		monitorSQLStep{match: "FOR UPDATE SKIP LOCKED LIMIT 1", columns: []string{"id"}},
	)
	a := &App{store: &store{db: db}}
	if err := a.updateImageJob(context.Background(), imageDownloadJob{ID: 1, ClaimToken: "token"}, map[string]any{"downloaded_bytes": 10}); err != nil {
		t.Fatal(err)
	}
	for _, part := range []string{"claim_token=", "locked_until>now()", "status IN ('downloading','verifying')"} {
		if !strings.Contains(conn.queries[0], part) {
			t.Errorf("missing lease fence %q", part)
		}
	}
	a.pollImageDownload(context.Background())
	if !strings.Contains(conn.queries[2], "RETURNING *") {
		t.Fatal("claim must return all job fields including source")
	}
	if !strings.Contains(conn.queries[1], "locked_until < now()") {
		t.Fatal("recovery must only fail expired jobs")
	}
}

func TestCatalogJobsIntegration(t *testing.T) {
	dsn := os.Getenv("PLATFORM_TEST_DSN")
	if dsn == "" {
		t.Skip("PLATFORM_TEST_DSN must point to a migrated disposable database")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	conn, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	tx := db.Begin()
	if tx.Error != nil {
		t.Fatal(tx.Error)
	}
	defer tx.Rollback()
	var sc scope
	if err := tx.Raw("SELECT p.tenant_id AS tenant, p.id AS project, u.id AS user FROM projects p CROSS JOIN users u WHERE p.deleted_at IS NULL AND p.status='active' ORDER BY p.id,u.id LIMIT 1").Scan(&sc).Error; err != nil {
		t.Fatal(err)
	}
	if sc.Project == 0 {
		t.Skip("requires a disposable project member fixture")
	}
	if err := tx.Exec("DELETE FROM image_download_jobs WHERE tenant_id=? AND project_id=?", sc.Tenant, sc.Project).Error; err != nil {
		t.Fatal(err)
	}
	a := &App{store: &store{db: tx}}
	call := func(post bool, sources ...string) catalogImage {
		t.Helper()
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("GET", "/", nil)
		c.Set("scope", sc)
		c.Params = gin.Params{{Key: "catalogId", Value: ubuntuCatalogID}}
		var body struct {
			Data json.RawMessage `json:"data"`
		}
		if post {
			if len(sources) > 0 {
				c.Request = httptest.NewRequest("POST", "/", strings.NewReader(fmt.Sprintf(`{"source":%q}`, sources[0])))
			}
			a.queueCatalogImage(c)
		} else {
			a.imageCatalog(c)
		}
		want := 200
		if post {
			want = 202
		}
		if w.Code != want {
			t.Fatalf("response %d: %s", w.Code, w.Body.String())
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		var entry catalogImage
		if post {
			err = json.Unmarshal(body.Data, &entry)
		} else {
			var list []catalogImage
			err = json.Unmarshal(body.Data, &list)
			if len(list) != 1 {
				t.Fatalf("catalog: %s", body.Data)
			}
			entry = list[0]
		}
		if err != nil {
			t.Fatal(err)
		}
		return entry
	}
	if got := call(false); got.Status != "available" || got.ImageID != nil {
		t.Fatalf("empty: %+v", got)
	}
	for i := 0; i < 2; i++ {
		if got := call(true); got.Status != "queued" {
			t.Fatalf("queued: %+v", got)
		}
	}
	if got := call(true, "tuna"); got.Source != "official" {
		t.Fatalf("queued source changed: %+v", got)
	}
	var count int64
	tx.Table("image_download_jobs").Where("tenant_id=? AND project_id=?", sc.Tenant, sc.Project).Count(&count)
	if count != 1 {
		t.Fatalf("duplicate jobs: %d", count)
	}
	job := imageDownloadJob{}
	tx.Table("image_download_jobs").Where("tenant_id=? AND project_id=?", sc.Tenant, sc.Project).Take(&job)
	if err := tx.Exec("UPDATE image_download_jobs SET status='downloading',claim_token='old',locked_until=now()-interval '1 minute' WHERE id=?", job.ID).Error; err != nil {
		t.Fatal(err)
	}
	job.ClaimToken = "old"
	if err := a.updateImageJob(context.Background(), job, map[string]any{"downloaded_bytes": 1}); err == nil {
		t.Fatal("expired lease renewed")
	}
	if err := tx.Exec("UPDATE image_download_jobs SET status='failed',claim_token=NULL,locked_until=NULL WHERE id=?", job.ID).Error; err != nil {
		t.Fatal(err)
	}
	if got := call(true, "tuna"); got.Status != "queued" || got.Source != "tuna" {
		t.Fatalf("retry: %+v", got)
	}
	if got := call(false); got.Source != "tuna" || got.SourceURL != ubuntuTunaBaseURL+ubuntuFilename {
		t.Fatalf("catalog source: %+v", got)
	}
	var imageID int64
	if err := a.store.saveResource(context.Background(), resource{table: "images"}, sc, 0, map[string]any{"name": ubuntuFilename, "object_key": fmt.Sprintf("test-catalog-%d", job.ID), "size_bytes": 1, "sha256": ubuntuSHA256, "os_type": "ubuntu", "version": "22.04.5", "architecture": "x86_64", "status": "ready"}, true, &imageID); err != nil {
		t.Fatal(err)
	}
	if err := tx.Exec("UPDATE image_download_jobs SET status='ready',image_id=? WHERE id=?", imageID, job.ID).Error; err != nil {
		t.Fatal(err)
	}
	if got := call(true); got.Status != "ready" || got.ImageID == nil || *got.ImageID != imageID {
		t.Fatalf("ready: %+v", got)
	}
	if err := tx.Exec("UPDATE images SET deleted_at=now() WHERE id=?", imageID).Error; err != nil {
		t.Fatal(err)
	}
	if got := call(false); got.Status != "failed" || got.ImageID != nil {
		t.Fatalf("deleted: %+v", got)
	}
	if got := call(true); got.Status != "queued" || got.ImageID != nil {
		t.Fatalf("deleted retry: %+v", got)
	}
}
