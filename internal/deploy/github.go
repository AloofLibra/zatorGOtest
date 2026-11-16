package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/AloofLibra/zator/internal/fetch"
	"github.com/AloofLibra/zator/internal/fsutil"
	"github.com/AloofLibra/zator/internal/platform"
)

// GitHubAPIBase — базовый URL GitHub API (переопределяется в тестах).
var GitHubAPIBase = "https://api.github.com"

type ghRelease struct {
	TagName     string    `json:"tag_name"`
	Name        string    `json:"name"`
	PublishedAt time.Time `json:"published_at"`
	Assets      []struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
		Size               int64  `json:"size"`
	} `json:"assets"`
}

// FetchGitHubLatest читает /repos/{owner}/{repo}/releases/latest и строит
// Latest для сравнения версий. Если в релизе есть latest.json — не сюда:
// основной путь остаётся FetchLatest через ассет latest.json.
func (u *Updater) FetchGitHubLatest(ctx context.Context) (*Latest, error) {
	if u.Offline {
		return nil, ErrOffline
	}
	owner, repo := ReleaseOwner(), ReleaseRepo()
	api := strings.TrimRight(GitHubAPIBase, "/") + "/repos/" + owner + "/" + repo + "/releases/latest"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, api, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", fetch.DefaultUserAgent)
	client := u.fetcher().HTTP
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxLatestBytes+1))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, &fetch.HTTPError{URL: api, Status: resp.StatusCode}
	}
	if len(body) > maxLatestBytes {
		return nil, fmt.Errorf("%w: слишком большой ответ GitHub API", ErrInvalidLatest)
	}
	var rel ghRelease
	if err := json.Unmarshal(body, &rel); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidLatest, err)
	}
	tag := strings.TrimSpace(rel.TagName)
	if tag == "" {
		return nil, fmt.Errorf("%w: пустой tag_name", ErrInvalidLatest)
	}
	buildDate := ""
	if !rel.PublishedAt.IsZero() {
		buildDate = rel.PublishedAt.UTC().Format("2006-01-02 15:04")
	}
	return &Latest{
		SchemaVersion: SchemaVersion,
		Release:       tag,
		BuildDate:     buildDate,
		ZatorDate:     buildDate,
		WebUIDate:     buildDate,
		Assets:        map[string]Asset{},
	}, nil
}

// binaryAssetName — имя ассета бинарника для текущей архитектуры устройства.
// Учитывает ReleaseArch (lexra → mipsel) и детект платформы, а не только GOARCH
// бинарника (на Lexra SelfArch() дал бы mips/mipsel без cpuinfo).
func binaryAssetName() string {
	arch := platform.Detect().Arch
	if arch == platform.ArchUnknown {
		arch = platform.SelfArch()
	}
	if arch == platform.ArchUnknown {
		switch runtime.GOARCH {
		case "amd64":
			arch = platform.ArchX86_64
		case "386":
			arch = platform.ArchX86
		case "arm64":
			arch = platform.ArchARM64
		case "arm":
			arch = platform.ArchARM
		case "riscv64":
			arch = platform.ArchRISCV64
		default:
			return ""
		}
	}
	if name := platform.BinaryAssetName(arch); name != "" {
		return name
	}
	return ""
}

// FindGitHubBinaryURL ищет URL zator-linux-<arch> в latest-релизе.
// sha — hex из ассета SHA256SUMS (пусто, если файла нет в релизе).
func (u *Updater) FindGitHubBinaryURL(ctx context.Context) (tag, url string, size int64, sha string, err error) {
	if u.Offline {
		return "", "", 0, "", ErrOffline
	}
	owner, repo := ReleaseOwner(), ReleaseRepo()
	want := binaryAssetName()
	if want == "" {
		return "", "", 0, "", fmt.Errorf("deploy: неподдерживаемая архитектура %q", runtime.GOARCH)
	}
	api := strings.TrimRight(GitHubAPIBase, "/") + "/repos/" + owner + "/" + repo + "/releases/latest"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, api, nil)
	if err != nil {
		return "", "", 0, "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", fetch.DefaultUserAgent)
	client := u.fetcher().HTTP
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", "", 0, "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxLatestBytes+1))
	if err != nil {
		return "", "", 0, "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", "", 0, "", &fetch.HTTPError{URL: api, Status: resp.StatusCode}
	}
	var rel ghRelease
	if err := json.Unmarshal(body, &rel); err != nil {
		return "", "", 0, "", err
	}
	tag = strings.TrimSpace(rel.TagName)
	var sumsURL string
	for _, a := range rel.Assets {
		switch {
		case a.Name == want && a.BrowserDownloadURL != "":
			url, size = a.BrowserDownloadURL, a.Size
		case a.Name == "SHA256SUMS" && a.BrowserDownloadURL != "":
			sumsURL = a.BrowserDownloadURL
		}
	}
	if url == "" {
		return tag, "", 0, "", fmt.Errorf("deploy: в релизе нет ассета %s", want)
	}
	if sumsURL != "" {
		sha, _ = u.fetchAssetSHA256(ctx, sumsURL, want)
	}
	return tag, url, size, sha, nil
}

func (u *Updater) fetchAssetSHA256(ctx context.Context, sumsURL, wantName string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, sumsURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", fetch.DefaultUserAgent)
	client := u.fetcher().HTTP
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", &fetch.HTTPError{URL: sumsURL, Status: resp.StatusCode}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	return parseSHA256SUMS(body, wantName), nil
}

func parseSHA256SUMS(body []byte, wantName string) string {
	for _, line := range strings.Split(string(body), "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) < 2 {
			continue
		}
		name := fields[len(fields)-1]
		name = strings.TrimPrefix(name, "*") // binary mode marker
		if name == wantName || filepath.Base(name) == wantName {
			sum := strings.ToLower(fields[0])
			if sha256Re.MatchString(sum) {
				return sum
			}
		}
	}
	return ""
}

// ApplyGitHubBinary скачивает zator-linux-<arch>, заменяет бинарник и
// запускает «zator install -zapret=false», чтобы подтянуть встроенные assets.
func (u *Updater) ApplyGitHubBinary(ctx context.Context) (*ApplyResult, string, error) {
	return u.applyGitHubBinary(ctx, "")
}

func (u *Updater) applyGitHubBinary(ctx context.Context, expectedVersion string) (*ApplyResult, string, error) {
	tag, link, size, sha, err := u.FindGitHubBinaryURL(ctx)
	if err != nil {
		return nil, "", err
	}
	if expectedVersion != "" && CompareVersions(tag, expectedVersion) != 0 {
		return nil, tag, fmt.Errorf("deploy: бинарник %s не совпадает с архивным релизом %s", tag, expectedVersion)
	}
	if link == "" {
		return nil, tag, fmt.Errorf("deploy: бинарник для этой arch не найден в релизе %s", tag)
	}
	dir := TempDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, tag, err
	}
	dest := filepath.Join(dir, binaryAssetName())
	if err := u.fetcher().Download(ctx, link, dest, sha); err != nil {
		return nil, tag, err
	}
	if size > 0 {
		st, err := os.Stat(dest)
		if err != nil {
			return nil, tag, err
		}
		if st.Size() != size {
			os.Remove(dest)
			return nil, tag, fmt.Errorf("%w: ожидалось %d, получено %d", ErrSize, size, st.Size())
		}
	}
	if err := os.Chmod(dest, 0o755); err != nil {
		return nil, tag, err
	}
	data, err := os.ReadFile(dest)
	if err != nil {
		return nil, tag, err
	}
	os.Remove(dest)
	if err := u.installBinary(ctx, data, tag, "latest"); err != nil {
		return nil, tag, err
	}
	return &ApplyResult{Installed: 1}, tag, nil
}

// ApplyBundleBinary устанавливает только бинарник, соответствующий архитектуре
// устройства, из vendor/ офлайн-бандла. SHA-256 берётся из соседнего .sha256.
func (u *Updater) ApplyBundleBinary(ctx context.Context, b Bundle) (*ApplyResult, error) {
	name := binaryAssetName()
	if name == "" {
		return nil, fmt.Errorf("deploy: неподдерживаемая архитектура %q", runtime.GOARCH)
	}
	source := filepath.Join(b.VendorDir, name)
	data, err := os.ReadFile(source)
	if err != nil {
		return nil, fmt.Errorf("deploy: в offline vendor нет %s: %w", name, err)
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("deploy: пустой offline бинарник %s", name)
	}
	sums, err := os.ReadFile(source + ".sha256")
	if err != nil {
		return nil, fmt.Errorf("deploy: нет checksum для offline бинарника %s: %w", name, err)
	}
	want := parseSHA256SUMS(sums, name)
	if !sha256Re.MatchString(want) {
		return nil, fmt.Errorf("deploy: checksum offline бинарника %s некорректен", name)
	}
	got, err := fetch.SHA256File(source)
	if err != nil {
		return nil, err
	}
	if !strings.EqualFold(got, want) {
		return nil, fmt.Errorf("deploy: offline binary checksum mismatch: %s", name)
	}
	if err := u.installBinary(ctx, data, "", "offline"); err != nil {
		return nil, err
	}
	return &ApplyResult{Installed: 1}, nil
}

func (u *Updater) installBinary(ctx context.Context, data []byte, tag, tracking string) error {
	binDir := u.Paths.BinDir
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return err
	}
	final := filepath.Join(binDir, "zator")
	if err := os.MkdirAll(TempDir(), 0o755); err != nil {
		return err
	}
	tmpFile, err := os.CreateTemp(TempDir(), "zator-new-*")
	if err != nil {
		return err
	}
	tmp := tmpFile.Name()
	if _, err := tmpFile.Write(data); err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := tmpFile.Chmod(0o755); err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := tmpFile.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	defer os.Remove(tmp)
	backup, hadBinary, err := snapshotFile(final)
	if err != nil {
		return err
	}
	versionPath := VersionPath(u.Paths)
	oldVersion, versionErr := os.ReadFile(versionPath)
	if versionErr != nil && !os.IsNotExist(versionErr) {
		if backup != "" {
			_ = os.Remove(backup)
		}
		return versionErr
	}
	hadVersion := versionErr == nil
	rollback := func(cause error) error {
		var undoErr error
		if hadBinary {
			undoErr = restoreSnapshot(backup, final)
		} else if err := os.Remove(final); err != nil && !os.IsNotExist(err) {
			undoErr = err
		}
		if hadVersion {
			undoErr = errors.Join(undoErr, fsutil.AtomicWriteFile(versionPath, oldVersion, 0o644))
		} else if err := os.Remove(versionPath); err != nil && !os.IsNotExist(err) {
			undoErr = errors.Join(undoErr, err)
		}
		return errors.Join(cause, undoErr)
	}
	if err := os.Rename(tmp, final); err != nil {
		// /tmp may be a different filesystem; keep the destination write atomic
		// through a same-directory short-lived file only as a cross-device fallback.
		dstTmp := final + ".new"
		if werr := os.WriteFile(dstTmp, data, 0o755); werr != nil {
			return rollback(fmt.Errorf("deploy: замена бинарника: %v; fallback: %w", err, werr))
		}
		if werr := os.Rename(dstTmp, final); werr != nil {
			_ = os.Remove(dstTmp)
			return rollback(werr)
		}
	}
	// Новый бинарник сам разложит актуальные assets (webui и пр.).
	// -skip-service: не трогаем units; auth из существующего zator.json сохраняется.
	var out []byte
	if u.runInstallCommand != nil {
		out, err = u.runInstallCommand(ctx, final)
	} else {
		cmd := exec.CommandContext(ctx, final, "install", "-zapret=false", "-skip-service")
		cmd.Env = os.Environ()
		out, err = cmd.CombinedOutput()
	}
	if err != nil {
		return rollback(fmt.Errorf("deploy: %s install: %w (%s)", final, err, strings.TrimSpace(string(out))))
	}
	// The self-install command may rewrite version.env from the binary's
	// embedded version. Keep the release metadata already applied from the
	// archive so commit/SHA checks remain meaningful after the binary swap.
	if hadVersion {
		if err := fsutil.AtomicWriteFile(versionPath, oldVersion, 0o644); err != nil {
			return rollback(fmt.Errorf("deploy: restore release metadata after self-install: %w", err))
		}
	}
	if strings.TrimSpace(tag) != "" {
		ver := strings.TrimPrefix(tag, "v")
		now := time.Now().UTC().Format("2006-01-02 15:04")
		current, err := ReadVersion(u.Paths)
		if err != nil {
			return rollback(fmt.Errorf("deploy: read release metadata after self-install: %w", err))
		}
		current.ZatorVersion = ver
		if current.ZatorDate == "" {
			current.ZatorDate = now
		}
		current.WebUIVersion = ver
		if current.WebUIDate == "" {
			current.WebUIDate = now
		}
		if tracking != "" {
			current.Tracking = tracking
		}
		if err := WriteVersion(u.Paths, current); err != nil {
			return rollback(err)
		}
	} else if tracking != "" {
		if current, err := ReadVersion(u.Paths); err == nil {
			current.Tracking = tracking
			if err := WriteVersion(u.Paths, current); err != nil {
				return rollback(err)
			}
		} else if !os.IsNotExist(err) {
			return rollback(err)
		}
	}
	if backup != "" {
		_ = os.Remove(backup)
	}
	return nil
}

// snapshotFile stores the previous binary backup under tmpfs.
func snapshotFile(path string) (string, bool, error) {
	st, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if !st.Mode().IsRegular() {
		return "", false, fmt.Errorf("deploy: нельзя обновить небезопасный файл %s", path)
	}
	if err := os.MkdirAll(TempDir(), 0o755); err != nil {
		return "", false, err
	}
	f, err := os.CreateTemp(TempDir(), "zator-rollback-*")
	if err != nil {
		return "", false, err
	}
	backup := f.Name()
	if err := f.Close(); err != nil {
		_ = os.Remove(backup)
		return "", false, err
	}
	if err := copyFile(path, backup, 0o700); err != nil {
		_ = os.Remove(backup)
		return "", false, fmt.Errorf("deploy: snapshot бинарника: %w", err)
	}
	return backup, true, nil
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	return errors.Join(copyErr, closeErr)
}

func restoreSnapshot(backup, target string) error {
	if err := os.Rename(backup, target); err == nil {
		return nil
	}
	tmp := target + ".restore"
	if err := copyFile(backup, tmp, 0o755); err != nil {
		return err
	}
	if err := os.Rename(tmp, target); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Remove(backup)
}
