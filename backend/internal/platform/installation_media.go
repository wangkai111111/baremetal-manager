package platform

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/minio/minio-go/v7"
)

type installationMedia struct {
	ID           int64  `json:"id"`
	Name         string `json:"name"`
	ObjectKey    string `json:"object_key"`
	SHA256       string `json:"sha256"`
	SizeBytes    int64  `json:"size_bytes"`
	OSType       string `json:"os_type"`
	Version      string `json:"version"`
	Architecture string `json:"architecture"`
	Status       string `json:"status"`
}

var errInstallMedia = errors.New("安装镜像准备失败")

var mediaDigestRE = regexp.MustCompile(`^[a-f0-9]{64}$`)
var isoReleaseRE = regexp.MustCompile(`^Ubuntu-Server ([0-9]+\.[0-9]+(?:\.[0-9]+)?) `)

var ubuntuLiveVersionRE = regexp.MustCompile(`^(22\.04|24\.04)(\.[0-9]+)?$`)

func (m installationMedia) unavailableReason() string {
	if m.Status != "ready" {
		return "镜像尚未就绪"
	}
	if strings.ToLower(m.OSType) != "ubuntu" || !ubuntuLiveVersionRE.MatchString(m.Version) {
		return "目前支持 Ubuntu Server 22.04 / 24.04 Live ISO"
	}
	if m.Architecture != "x86_64" && m.Architecture != "amd64" {
		return "测试 VM 需要 x86_64 / amd64 镜像"
	}
	if !mediaDigestRE.MatchString(m.SHA256) || m.SizeBytes <= 0 || m.SizeBytes > 20<<30 || m.ObjectKey == "" {
		return "镜像校验信息不完整"
	}
	return ""
}
func mediaDirectory(root string, m *installationMedia) (string, error) {
	if m == nil || !mediaDigestRE.MatchString(m.SHA256) {
		return "", errors.New("镜像校验信息无效")
	}
	return provisionResolveUnder(root, "ipxe/install-media/"+m.SHA256)
}
func regularMediaFile(path string) bool {
	st, err := os.Lstat(path)
	return err == nil && st.Mode().IsRegular() && st.Size() > 0
}

// Content-addressed, atomic cache: never pair an ISO with another release's kernel.
func prepareInstallationMedia(ctx context.Context, root string, m *installationMedia) error {
	if reason := m.unavailableReason(); reason != "" {
		return errors.New(reason)
	}
	dir, err := mediaDirectory(root, m)
	if err != nil {
		return err
	}
	if st, e := os.Lstat(dir); e == nil && !st.IsDir() {
		return errors.New("安装介质缓存路径无效")
	}
	manifest, _ := os.ReadFile(filepath.Join(dir, "ready.json"))
	var ready struct {
		SHA256 string
		Size   int64
	}
	if json.Unmarshal(manifest, &ready) == nil && ready.SHA256 == m.SHA256 && ready.Size == m.SizeBytes && regularMediaFile(filepath.Join(dir, "image.iso")) && regularMediaFile(filepath.Join(dir, "vmlinuz")) && regularMediaFile(filepath.Join(dir, "initrd")) {
		return nil
	}
	parent := filepath.Dir(dir)
	if err = os.MkdirAll(parent, 0755); err != nil {
		return errors.New("无法创建安装介质目录")
	}
	tmp, err := os.MkdirTemp(parent, ".prepare-")
	if err != nil {
		return errors.New("无法准备安装介质")
	}
	defer os.RemoveAll(tmp)
	var source io.ReadCloser
	if strings.HasPrefix(m.ObjectKey, "file://") {
		isoRoot := filepath.Join(root, "iso")
		path := strings.TrimPrefix(m.ObjectKey, "file://")
		relative, e := filepath.Rel(isoRoot, path)
		if e != nil || strings.HasPrefix(relative, "..") || filepath.IsAbs(relative) {
			return errors.New("本地镜像不在受管 ISO 目录内")
		}
		safe, e := provisionResolveUnder(isoRoot, relative)
		if e != nil || !regularMediaFile(safe) {
			return errors.New("本地安装镜像不可用")
		}
		source, err = os.Open(safe)
	} else {
		client, bucket, e := objectStorage()
		if e != nil || bucket == "" {
			return errors.New("镜像存储不可用")
		}
		source, err = client.GetObject(ctx, bucket, m.ObjectKey, minio.GetObjectOptions{})
	}
	if err != nil {
		return errors.New("无法读取所选安装镜像")
	}
	defer source.Close()
	out, err := os.OpenFile(filepath.Join(tmp, "image.iso"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		return errors.New("无法缓存安装镜像")
	}
	hash := sha256.New()
	count, copyErr := io.Copy(io.MultiWriter(out, hash), io.LimitReader(&contextMediaReader{ctx: ctx, r: source}, m.SizeBytes+1))
	closeErr := out.Close()
	if copyErr != nil || closeErr != nil || count != m.SizeBytes || hex.EncodeToString(hash.Sum(nil)) != m.SHA256 {
		return errors.New("所选镜像大小或 SHA256 校验失败")
	}
	infoPath := filepath.Join(tmp, "iso-info")
	if err := exec.CommandContext(ctx, "xorriso", "-osirrox", "on", "-indev", filepath.Join(tmp, "image.iso"), "-extract", "/.disk/info", infoPath).Run(); err != nil {
		return errors.New("镜像缺少 Ubuntu Server 发行信息")
	}
	infoStat, err := os.Lstat(infoPath)
	if err != nil || !infoStat.Mode().IsRegular() || infoStat.Size() > 4096 {
		return errors.New("ISO 发行信息无效")
	}
	info, _ := os.ReadFile(infoPath)
	release := isoReleaseRE.FindStringSubmatch(string(info))
	versionMatches := len(release) == 2 && (release[1] == m.Version || ((m.Version == "22.04" || m.Version == "24.04") && strings.HasPrefix(release[1], m.Version+".")))
	if !versionMatches || !strings.Contains(string(info), " amd64 ") {
		return errors.New("ISO 实际发行版本或架构与镜像信息不符，请选择匹配的 Ubuntu Live Server ISO")
	}
	for _, name := range []string{"vmlinuz", "initrd"} {
		cmd := exec.CommandContext(ctx, "xorriso", "-osirrox", "on", "-indev", filepath.Join(tmp, "image.iso"), "-extract", "/casper/"+name, filepath.Join(tmp, name))
		// xorriso output is intentionally not logged: source metadata is private.
		if err := cmd.Run(); err != nil || !regularMediaFile(filepath.Join(tmp, name)) {
			return errors.New("镜像缺少 casper 内核/initrd，请选择 Ubuntu Live Server ISO")
		}
		if err := os.Chmod(filepath.Join(tmp, name), 0644); err != nil {
			return errors.New("安装介质权限设置失败")
		}
	}
	data, _ := json.Marshal(struct {
		SHA256 string
		Size   int64
	}{m.SHA256, m.SizeBytes})
	if err := os.WriteFile(filepath.Join(tmp, "ready.json"), data, 0644); err != nil {
		return errors.New("无法记录介质校验结果")
	}
	// A fully published cache is immutable. Incomplete caches are never served.
	if err := os.Rename(tmp, dir); err != nil {
		return errors.New("安装介质缓存发布失败，请检查磁盘空间及缓存目录")
	}
	return nil
}

type contextMediaReader struct {
	ctx context.Context
	r   io.Reader
}

func (r *contextMediaReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(p)
}

func installationMediaPath(root string, snap installationSnapshot, kind, name string) (string, error) {
	if snap.Media == nil {
		if (kind == "netboot" && (name == "vmlinuz" || name == "initrd")) || (kind == "iso" && name == DefaultISOFileName) {
			return provisionResolveUnder(filepath.Join(root, kind), name)
		}
	} else if (kind == "netboot" && (name == "vmlinuz" || name == "initrd")) || (kind == "iso" && name == "image.iso") {
		dir, err := mediaDirectory(root, snap.Media)
		if err != nil {
			return "", err
		}
		return provisionResolveUnder(dir, name)
	}
	return "", errors.New("media not found")
}
