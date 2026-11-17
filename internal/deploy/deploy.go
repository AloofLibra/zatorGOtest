// Package deploy — самообновление zator из релизных tar.gz-архивов.
//
// Поток обновления:
//
//  1. CheckLatest скачивает latest.json (URL настраивается) и сравнивает
//     SHA сборки с установленной версией (version.env).
//  2. DownloadAsset скачивает архив варианта (core|webui|full) и проверяет
//     размер и sha256 из latest.json.
//  3. Unpack безопасно распаковывает архив в staging-каталог
//     (fetch.SafeUntar), а Apply сверяет sha256 всех файлов с манифестом
//     (extra_strats/cache/deploy/manifest.tsv) и только затем раскладывает
//     файлы по назначению. Классы файлов манифеста:
//     auto / payload — всегда заменяются (при совпадении sha256 пропускаются),
//     keep-if-exists — кладутся, только если файла нет (пользовательские списки).
//     При ошибке в середине применения заменённые файлы возвращаются обратно.
//
// Офлайн-режим: переменная Z2R_OFFLINE=1 либо каталог бандла (vendor/ и
// zator-*.tar.gz) отключают сетевые операции; бандл применяется через
// ApplyBundle.
package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/AloofLibra/zator/internal/fetch"
	"github.com/AloofLibra/zator/internal/fsutil"
	"github.com/AloofLibra/zator/internal/paths"
	"github.com/AloofLibra/zator/internal/platform"
)

// Источники и настройки по умолчанию.
const (
	// DefaultOwner и DefaultRepo — репозиторий релизов ZatorGO.
	DefaultOwner = "Koviand"
	DefaultRepo  = "zatorGO"
	// EnvOwner / EnvRepo переопределяют репозиторий релизов (как в лаунчере).
	EnvOwner = "ZATOR_OWNER"
	EnvRepo  = "ZATOR_REPO"
	// EnvLatestURL переопределяет адрес latest.json.
	EnvLatestURL = "ZATOR_LATEST_URL"
	// ManifestRel — путь манифеста внутри архива.
	ManifestRel = "extra_strats/cache/deploy/manifest.tsv"
	// VersionEnvRel — путь version.env внутри архива.
	VersionEnvRel = "extra_strats/cache/deploy/version.env"

	// Классы файлов манифеста.
	ClassAuto               = "auto"
	ClassKeepIfExists       = "keep-if-exists"
	ClassPayload            = "payload"
	ClassUpdateIfUnmodified = "update-if-unmodified"

	// Варианты архива.
	VariantCore   = "core"
	VariantWebUI  = "webui"
	VariantFull   = "full"
	VariantApp    = "app"
	VariantConfig = "config"
	ModeApp       = "app"
	ModeConfig    = "config"
	ModeFull      = "full"

	// SchemaVersion — поддерживаемая версия формата latest.json.
	SchemaVersion = 1

	maxLatestBytes = 1 << 20
)

// DefaultLatestURL — адрес latest.json по умолчанию (репозиторий Koviand/zatorGO).
var DefaultLatestURL = "https://github.com/" + DefaultOwner + "/" + DefaultRepo + "/releases/latest/download/latest.json"

// ReleaseOwner / ReleaseRepo возвращают owner/repo релизов с учётом env.
func ReleaseOwner() string {
	if v := strings.TrimSpace(os.Getenv(EnvOwner)); v != "" {
		return v
	}
	return DefaultOwner
}

func ReleaseRepo() string {
	if v := strings.TrimSpace(os.Getenv(EnvRepo)); v != "" {
		return v
	}
	return DefaultRepo
}

// LatestJSONURL — URL latest.json для текущего owner/repo (или ZATOR_LATEST_URL).
func LatestJSONURL() string {
	if v := strings.TrimSpace(os.Getenv(EnvLatestURL)); v != "" {
		return v
	}
	if ReleaseOwner() == DefaultOwner && ReleaseRepo() == DefaultRepo {
		return DefaultLatestURL
	}
	return "https://github.com/" + ReleaseOwner() + "/" + ReleaseRepo() + "/releases/latest/download/latest.json"
}

// Ошибки пакета.
var (
	// ErrOffline — сетевая операция в офлайн-режиме.
	ErrOffline = errors.New("deploy: офлайн-режим, сеть недоступна")
	// ErrInvalidLatest — latest.json не прошёл проверку.
	ErrInvalidLatest = errors.New("deploy: некорректный latest.json")
	// ErrUnknownVariant — неизвестный вариант архива.
	ErrUnknownVariant = errors.New("deploy: неизвестный вариант архива")
	// ErrNoManifest — в архиве нет манифеста.
	ErrNoManifest = errors.New("deploy: в архиве нет манифеста")
	// ErrBadManifest — манифест некорректен или содержит недопустимые пути.
	ErrBadManifest = errors.New("deploy: некорректный манифест")
	// ErrVerify — файл staging не совпал с sha256 манифеста.
	ErrVerify = errors.New("deploy: сверка staging не пройдена")
	// ErrSize — размер скачанного архива не совпал с latest.json.
	ErrSize = errors.New("deploy: размер архива не совпал")
)

// ValidVariant сообщает, что variant — core, webui или full.
func ValidVariant(v string) bool {
	return v == VariantCore || v == VariantWebUI || v == VariantFull || v == VariantApp || v == VariantConfig
}

// ---------------------------------------------------------------------------
// latest.json

// Asset — архив релиза в latest.json.
type Asset struct {
	Name         string `json:"name"`
	Size         int64  `json:"size"`
	SHA256       string `json:"sha256"`
	UnpackedSize int64  `json:"unpackedSize"`
	URL          string `json:"url,omitempty"`
}

// Latest — указатель последней сборки (latest.json).
type Latest struct {
	SchemaVersion int              `json:"schemaVersion"`
	Release       string           `json:"release"`
	BuildDate     string           `json:"buildDate"`
	Commit        string           `json:"commit"`
	ZatorSHA      string           `json:"zatorSha"`
	WebUISHA      string           `json:"webuiSha"`
	ConfigSHA     string           `json:"configSha"`
	ZatorDate     string           `json:"zatorDate"`
	WebUIDate     string           `json:"webuiDate"`
	Assets        map[string]Asset `json:"assets"`
}

var sha256Re = regexp.MustCompile(`^[0-9a-fA-F]{64}$`)

// ParseLatest разбирает и проверяет latest.json.
func ParseLatest(data []byte) (*Latest, error) {
	var l Latest
	if err := json.Unmarshal(data, &l); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidLatest, err)
	}
	if l.SchemaVersion != SchemaVersion {
		return nil, fmt.Errorf("%w: schemaVersion %d", ErrInvalidLatest, l.SchemaVersion)
	}
	if strings.TrimSpace(l.Release) == "" {
		return nil, fmt.Errorf("%w: пустой release", ErrInvalidLatest)
	}
	for name, a := range l.Assets {
		if !ValidVariant(name) {
			continue
		}
		if a.Size < 0 || a.UnpackedSize < 0 || (a.SHA256 != "" && !sha256Re.MatchString(a.SHA256)) {
			return nil, fmt.Errorf("%w: ассет %s", ErrInvalidLatest, name)
		}
		if !safeBaseName(a.Name) {
			return nil, fmt.Errorf("%w: имя ассета %q", ErrInvalidLatest, a.Name)
		}
		if a.URL != "" {
			u, err := url.Parse(a.URL)
			if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
				return nil, fmt.Errorf("%w: url ассета %s", ErrInvalidLatest, name)
			}
		}
	}
	return &l, nil
}

func safeBaseName(n string) bool {
	return n != "" && n != "." && n != ".." && !strings.ContainsAny(n, `/\`) && !strings.ContainsRune(n, 0) && filepath.Base(n) == n
}

// Asset возвращает ассет варианта.
func (l *Latest) Asset(variant string) (Asset, error) {
	if !ValidVariant(variant) {
		return Asset{}, fmt.Errorf("%w: %q", ErrUnknownVariant, variant)
	}
	a, ok := l.Assets[variant]
	if !ok || a.Name == "" || a.SHA256 == "" || a.Size <= 0 {
		return Asset{}, fmt.Errorf("%w: в latest.json нет полного ассета %q", ErrInvalidLatest, variant)
	}
	return a, nil
}

// ---------------------------------------------------------------------------
// Updater

// Updater выполняет проверку, загрузку и применение обновлений.
type Updater struct {
	Paths paths.Paths
	// Fetch — загрузчик (повторы, зеркала, sha256). По умолчанию fetch.NewClient().
	Fetch *fetch.Client
	// LatestURL — адрес latest.json (по умолчанию DefaultLatestURL или
	// ZATOR_LATEST_URL).
	LatestURL string
	// ScriptDest — путь, куда ставится запись манифеста с dest=/opt/z2r.sh.
	// Пусто — такие записи пропускаются.
	ScriptDest string
	// Remap преобразует относительный путь внутри /opt/zator в путь относительно
	// ZatorRoot (nil — без изменений). Пустой результат пропускает файл.
	Remap func(rel string) string
	// Offline отключает сетевые операции.
	Offline bool
	// WebUIInstalled сообщает, установлен ли веб-интерфейс (по умолчанию —
	// WebUIDir существует и не пуст).
	WebUIInstalled func() bool
	// Now — источник времени (по умолчанию time.Now).
	Now func() time.Time
	// applyBinary is a test seam; production always fetches the matching release binary.
	applyBinary func(context.Context) (*ApplyResult, string, error)
	// runInstallCommand is a test seam for the post-replacement self-install.
	runInstallCommand func(context.Context, string) ([]byte, error)
}

// New создаёт обновлятор для раскладки p. Адрес latest.json берётся из
// ZATOR_LATEST_URL / ZATOR_OWNER+ZATOR_REPO, офлайн-флаг — из Z2R_OFFLINE.
func New(p paths.Paths) *Updater {
	arch := platform.ReleaseArch(platform.Detect().Arch)
	if arch == platform.ArchUnknown {
		arch = platform.ReleaseArch(platform.SelfArch())
	}
	snifferArch := string(arch)
	switch arch {
	case platform.ArchX86_64:
		snifferArch = "amd64"
	case platform.ArchX86:
		snifferArch = "386"
	case platform.ArchMIPSEL:
		snifferArch = "mipsle"
	}
	return &Updater{
		Paths:     p,
		Fetch:     fetch.NewClient(),
		LatestURL: LatestJSONURL(),
		Offline:   DetectOffline(os.Getenv, "").Offline,
		Remap: func(rel string) string {
			const prefix = "tools/bin/googlevideo-dns-sniffer-"
			if strings.HasPrefix(rel, prefix) {
				if strings.TrimPrefix(rel, prefix) != snifferArch {
					return ""
				}
				return "tools/bin/googlevideo-dns-sniffer"
			}
			return rel
		},
	}
}

func (u *Updater) now() time.Time {
	if u.Now != nil {
		return u.Now()
	}
	return time.Now()
}

func (u *Updater) fetcher() *fetch.Client {
	if u.Fetch != nil {
		return u.Fetch
	}
	return fetch.NewClient()
}

func (u *Updater) latestURL() string {
	if u.LatestURL != "" {
		return u.LatestURL
	}
	return LatestJSONURL()
}

func (u *Updater) webUIInstalled() bool {
	if u.WebUIInstalled != nil {
		return u.WebUIInstalled()
	}
	ents, err := os.ReadDir(u.Paths.WebUIDir)
	return err == nil && len(ents) > 0
}

func readFileOpt(p string) ([]byte, error) {
	b, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	return b, err
}

// CheckResult — итог проверки обновлений.
type CheckResult struct {
	Latest  *Latest
	Current Version
	// UpdateZator — ядро устарело; UpdateWebUI — устарела веб-панель
	// (учитывается только при установленной панели).
	UpdateZator  bool
	UpdateWebUI  bool
	UpdateConfig bool
	// VersionNewer — тег релиза новее установленной версии (справочно).
	VersionNewer bool
	CheckedAt    time.Time
}

// UpdateAvailable сообщает, что есть что обновлять.
func (r *CheckResult) UpdateAvailable() bool {
	return r != nil && (r.UpdateZator || r.UpdateWebUI || r.UpdateConfig)
}

// CompareLatest сравнивает установленную версию с latest. Основной признак —
// несовпадение SHA содержимого (как в shell-версии); при неизвестном SHA ядра
// используется сравнение версий. SHA сравниваются только если заданы с обеих
// сторон — иначе GitHub API без SHA не даёт «вечный update».
func CompareLatest(cur Version, l *Latest, webUIInstalled bool) CheckResult {
	r := CheckResult{Latest: l, Current: cur}
	if l == nil {
		return r
	}
	// A release tag is immutable: matching installed and published versions
	// must not be offered again just because build metadata has a different SHA.
	sameRelease := knownVersion(cur.ZatorVersion) && strings.EqualFold(strings.TrimPrefix(strings.TrimSpace(cur.ZatorVersion), "v"), strings.TrimPrefix(strings.TrimSpace(l.Release), "v"))
	switch {
	case sameRelease:
		r.UpdateZator = false
	case cur.ZatorSHA != "" && l.ZatorSHA != "":
		r.UpdateZator = !strings.EqualFold(cur.ZatorSHA, l.ZatorSHA)
	case knownVersion(cur.ZatorVersion):
		r.UpdateZator = IsNewer(cur.ZatorVersion, l.Release)
	default:
		// Чистая установка без version.env — предложить обновление, если есть тег.
		r.UpdateZator = knownVersion(l.Release)
	}
	if webUIInstalled {
		switch {
		case sameRelease:
			r.UpdateWebUI = false
		case cur.WebUISHA != "" && l.WebUISHA != "":
			r.UpdateWebUI = !strings.EqualFold(cur.WebUISHA, l.WebUISHA)
		case knownVersion(cur.WebUIVersion):
			r.UpdateWebUI = IsNewer(cur.WebUIVersion, l.Release)
		case knownVersion(cur.ZatorVersion):
			// В binary-релизах Web UI едет вместе с ядром.
			r.UpdateWebUI = IsNewer(cur.ZatorVersion, l.Release)
		default:
			r.UpdateWebUI = knownVersion(l.Release)
		}
	}
	if l.ConfigSHA != "" {
		r.UpdateConfig = !strings.EqualFold(cur.ConfigSHA, l.ConfigSHA)
	}
	r.VersionNewer = knownVersion(cur.ZatorVersion) && IsNewer(cur.ZatorVersion, l.Release)
	return r
}

func knownVersion(v string) bool {
	v = strings.TrimSpace(v)
	return v != "" && v != "unknown" && v != "branch"
}

// FetchLatest скачивает и разбирает latest.json. Если ассета нет — fallback
// на GitHub Releases API репозитория Koviand/zatorGO (или ZATOR_OWNER/REPO).
// В офлайн-режиме — ErrOffline.
func (u *Updater) FetchLatest(ctx context.Context) (*Latest, []byte, error) {
	if u.Offline {
		return nil, nil, ErrOffline
	}
	dir := TempDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, nil, err
	}
	tmp, err := os.CreateTemp(dir, "latest-*.json.part")
	if err != nil {
		return nil, nil, err
	}
	tmpName := tmp.Name()
	tmp.Close()
	defer os.Remove(tmpName)
	if err := u.fetcher().Download(ctx, u.latestURL(), tmpName, ""); err == nil {
		f, err := os.Open(tmpName)
		if err != nil {
			return nil, nil, err
		}
		defer f.Close()
		data, err := io.ReadAll(io.LimitReader(f, maxLatestBytes+1))
		if err != nil {
			return nil, nil, err
		}
		if len(data) > maxLatestBytes {
			return nil, nil, fmt.Errorf("%w: слишком большой файл", ErrInvalidLatest)
		}
		l, err := ParseLatest(data)
		if err != nil {
			return nil, nil, err
		}
		return l, data, nil
	}
	// Нет latest.json в релизе — берём тег из GitHub API своего репозитория.
	l, err := u.FetchGitHubLatest(ctx)
	if err != nil {
		return nil, nil, err
	}
	raw, _ := json.Marshal(l)
	return l, raw, nil
}

// CheckLatest проверяет наличие обновления и кэширует latest.json.
func (u *Updater) CheckLatest(ctx context.Context) (*CheckResult, error) {
	l, raw, err := u.FetchLatest(ctx)
	if err != nil {
		return nil, err
	}
	cur, err := ReadVersion(u.Paths)
	if err != nil {
		return nil, err
	}
	// Если version.env ещё нет — сравниваем с версией бинарника из status API
	// через ZATOR_VERSION env / файл VERSION недоступен здесь; оставляем
	// CompareLatest: без SHA и без версии обновиться предложит при любом релизе.
	res := CompareLatest(cur, l, u.webUIInstalled())
	res.CheckedAt = u.now()
	if len(raw) > 0 {
		cachePath := LatestCachePath(u.Paths)
		_ = os.MkdirAll(filepath.Dir(cachePath), 0o755)
		_ = fsutil.AtomicWriteFile(cachePath, raw, 0o644)
	}
	return &res, nil
}

// AssetURL возвращает адрес архива ассета: url из latest.json либо
// <каталог latest.json>/<name>.
func (u *Updater) AssetURL(a Asset) (string, error) {
	if a.URL != "" {
		return a.URL, nil
	}
	base, err := url.Parse(u.latestURL())
	if err != nil {
		return "", err
	}
	base.Path = path.Join(path.Dir(base.Path), a.Name)
	base.RawQuery = ""
	return base.String(), nil
}

// DownloadAsset скачивает архив варианта в StateDir/deploy и проверяет размер
// и sha256 из latest.json. Возвращает путь к файлу.
func (u *Updater) DownloadAsset(ctx context.Context, l *Latest, variant string) (string, error) {
	if u.Offline {
		return "", ErrOffline
	}
	a, err := l.Asset(variant)
	if err != nil {
		return "", err
	}
	link, err := u.AssetURL(a)
	if err != nil {
		return "", err
	}
	dir := TempDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	dest := filepath.Join(dir, a.Name)
	if err := u.fetcher().Download(ctx, link, dest, a.SHA256); err != nil {
		return "", err
	}
	st, err := os.Stat(dest)
	if err != nil {
		return "", err
	}
	if st.Size() != a.Size {
		os.Remove(dest)
		return "", fmt.Errorf("%w: ожидалось %d, получено %d", ErrSize, a.Size, st.Size())
	}
	return dest, nil
}

// Unpack безопасно распаковывает архив во временный staging-каталог и
// возвращает его путь (вызывающий удаляет каталог).
func (u *Updater) Unpack(archive string) (string, error) {
	dir := TempDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	staging, err := os.MkdirTemp(dir, "staging-")
	if err != nil {
		return "", err
	}
	if err := fetch.SafeUntar(archive, staging); err != nil {
		os.RemoveAll(staging)
		return "", err
	}
	return staging, nil
}

// ---------------------------------------------------------------------------
// Манифест

// ManifestEntry — строка манифеста: path|dest|class|sha256|size|exec.
type ManifestEntry struct {
	Path   string
	Dest   string
	Class  string
	SHA256 string
	Size   int64
	Exec   bool
}

// ParseManifest разбирает manifest.tsv.
func ParseManifest(data []byte) ([]ManifestEntry, error) {
	var out []ManifestEntry
	seen := map[string]bool{}
	for i, line := range strings.Split(string(data), "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Split(line, "|")
		if len(f) != 6 {
			return nil, fmt.Errorf("%w: строка %d: %d полей", ErrBadManifest, i+1, len(f))
		}
		rel, err := fetch.CleanEntryName(f[0])
		if err != nil || rel == "." {
			return nil, fmt.Errorf("%w: строка %d: путь %q", ErrBadManifest, i+1, f[0])
		}
		switch f[2] {
		case ClassAuto, ClassKeepIfExists, ClassPayload, ClassUpdateIfUnmodified:
		default:
			return nil, fmt.Errorf("%w: строка %d: класс %q", ErrBadManifest, i+1, f[2])
		}
		if !sha256Re.MatchString(f[3]) {
			return nil, fmt.Errorf("%w: строка %d: sha256", ErrBadManifest, i+1)
		}
		var size int64
		if _, err := fmt.Sscanf(f[4], "%d", &size); err != nil || size < 0 {
			return nil, fmt.Errorf("%w: строка %d: размер %q", ErrBadManifest, i+1, f[4])
		}
		if f[5] != "0" && f[5] != "1" {
			return nil, fmt.Errorf("%w: строка %d: exec %q", ErrBadManifest, i+1, f[5])
		}
		if seen[rel] {
			return nil, fmt.Errorf("%w: дубликат пути %q", ErrBadManifest, rel)
		}
		seen[rel] = true
		out = append(out, ManifestEntry{
			Path: rel, Dest: f[1], Class: f[2], SHA256: strings.ToLower(f[3]), Size: size, Exec: f[5] == "1",
		})
	}
	return out, nil
}

// ScriptManifestDest — dest записи с лаунчером z2r.sh.
const ScriptManifestDest = "/opt/z2r.sh"

const zatorManifestPrefix = "/opt/zator/"
const zapretManifestPrefix = "/opt/zapret2/"

// resolveDest преобразует dest манифеста в путь на диске. skip=true — запись
// не применяется (нет ScriptDest или Remap вернул пусто).
func (u *Updater) resolveDest(dest string) (target string, skip bool, err error) {
	if dest == "" || strings.ContainsRune(dest, 0) || strings.Contains(dest, `\`) {
		return "", false, fmt.Errorf("%w: dest %q", ErrBadManifest, dest)
	}
	for _, seg := range strings.Split(dest, "/") {
		if seg == ".." {
			return "", false, fmt.Errorf("%w: dest %q", ErrBadManifest, dest)
		}
	}
	switch {
	case dest == ScriptManifestDest:
		if u.ScriptDest == "" {
			return "", true, nil
		}
		return u.ScriptDest, false, nil
	case strings.HasPrefix(dest, zapretManifestPrefix):
		rel := path.Clean(strings.TrimPrefix(dest, zapretManifestPrefix))
		if rel == "." || rel == "" || strings.HasPrefix(rel, "/") || rel == ".." || strings.HasPrefix(rel, "../") {
			return "", false, fmt.Errorf("%w: dest %q", ErrBadManifest, dest)
		}
		root := u.Paths.ZapretRoot
		target := filepath.Join(root, filepath.FromSlash(rel))
		if r, err := filepath.Rel(root, target); err != nil || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) {
			return "", false, fmt.Errorf("%w: dest %q", ErrBadManifest, dest)
		}
		return target, false, nil
	case strings.HasPrefix(dest, zatorManifestPrefix):
		rel := path.Clean(strings.TrimPrefix(dest, zatorManifestPrefix))
		if rel == "." || rel == "" || strings.HasPrefix(rel, "/") {
			return "", false, fmt.Errorf("%w: dest %q", ErrBadManifest, dest)
		}
		if u.Remap != nil {
			rel = u.Remap(rel)
			if rel == "" {
				return "", true, nil
			}
			if rel = path.Clean(rel); rel == "." || strings.HasPrefix(rel, "/") || rel == ".." || strings.HasPrefix(rel, "../") {
				return "", false, fmt.Errorf("%w: remap %q", ErrBadManifest, dest)
			}
		}
		root := u.Paths.ZatorRoot
		target = filepath.Join(root, filepath.FromSlash(rel))
		if r, err := filepath.Rel(root, target); err != nil || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) {
			return "", false, fmt.Errorf("%w: dest %q", ErrBadManifest, dest)
		}
		return target, false, nil
	}
	return "", false, fmt.Errorf("%w: недопустимый dest %q", ErrBadManifest, dest)
}

// LoadManifest читает манифест из staging.
func LoadManifest(staging string) ([]ManifestEntry, error) {
	data, err := os.ReadFile(filepath.Join(staging, filepath.FromSlash(ManifestRel)))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrNoManifest
		}
		return nil, err
	}
	return ParseManifest(data)
}

// VerifyStaging сверяет sha256 и размер каждого файла манифеста в staging.
func VerifyStaging(staging string, entries []ManifestEntry) error {
	var bad []string
	for _, e := range entries {
		p := filepath.Join(staging, filepath.FromSlash(e.Path))
		st, err := os.Stat(p)
		if err != nil || !st.Mode().IsRegular() {
			bad = append(bad, e.Path)
			continue
		}
		sum, err := fetch.SHA256File(p)
		if err != nil || sum != e.SHA256 || (e.Size > 0 && st.Size() != e.Size) {
			bad = append(bad, e.Path)
		}
	}
	if len(bad) > 0 {
		sort.Strings(bad)
		return fmt.Errorf("%w: повреждено файлов %d из %d (%s)", ErrVerify, len(bad), len(entries), strings.Join(bad, ", "))
	}
	return nil
}

// ---------------------------------------------------------------------------
// Применение

// ApplyOptions настраивает Apply.
type ApplyOptions struct {
	Component string
	// Tracking — значение TRACKING в version.env ("latest" или тег; пусто —
	// сохраняется прежнее, иначе latest).
	Tracking string
	// Meta — данные latest.json; используются для version.env, если в архиве
	// нет extra_strats/cache/deploy/version.env.
	Meta *Latest
	// deferCommit keeps rollback copies until the caller commits the larger
	// archive-plus-binary update.
	deferCommit bool
}

// ApplyResult — итог применения.
type ApplyResult struct {
	Installed int // записано файлов
	Unchanged int // пропущено: sha256 совпал
	Kept      int // пропущено: keep-if-exists и файл есть
	Skipped   int // пропущено: dest вне раскладки (нет ScriptDest, Remap → "")
	// Files — пути записанных файлов.
	Files []string
	// HasCore и HasWebUI — что именно принёс архив.
	HasCore, HasWebUI bool
	// ScriptUpdated — обновлён лаунчер (ScriptDest).
	ScriptUpdated bool
	Version       Version
	finish        func(commit bool) error
}

func (r *ApplyResult) finishTransaction(commit bool) error {
	if r == nil || r.finish == nil {
		return nil
	}
	finish := r.finish
	r.finish = nil
	return finish(commit)
}

// archiveTransaction collects deferred archive writes and owns their commit or rollback.
type archiveTransaction struct {
	results []*ApplyResult
	result  *ApplyResult
}

type archiveSpec struct {
	file, tracking, component string
	meta                      *Latest
}

func (u *Updater) applyArchives(specs []archiveSpec) (*archiveTransaction, error) {
	tx := &archiveTransaction{}
	for _, spec := range specs {
		res, err := u.ApplyArchive(spec.file, ApplyOptions{
			Tracking: spec.tracking, Meta: spec.meta, Component: spec.component, deferCommit: true,
		})
		if err != nil {
			return tx, tx.rollback(err)
		}
		tx.add(res)
	}
	return tx, nil
}

func (t *archiveTransaction) add(r *ApplyResult) {
	t.results = append(t.results, r)
	if t.result == nil {
		t.result = r
		return
	}
	t.result.Installed += r.Installed
	t.result.Unchanged += r.Unchanged
	t.result.Kept += r.Kept
	t.result.Skipped += r.Skipped
	t.result.HasCore = t.result.HasCore || r.HasCore
	t.result.HasWebUI = t.result.HasWebUI || r.HasWebUI
	t.result.Files = append(t.result.Files, r.Files...)
}

func (t *archiveTransaction) finish(commit bool) error {
	var errs []error
	if commit {
		for _, r := range t.results {
			errs = append(errs, r.finishTransaction(true))
		}
	} else {
		for i := len(t.results) - 1; i >= 0; i-- {
			errs = append(errs, t.results[i].finishTransaction(false))
		}
	}
	return errors.Join(errs...)
}

func (t *archiveTransaction) rollback(cause error) error {
	return errors.Join(cause, t.finish(false))
}

func (u *Updater) planFiles(staging string) ([]plannedFile, *ApplyResult, map[string]string, error) {
	entries, err := LoadManifest(staging)
	if err != nil {
		return nil, nil, nil, err
	}
	if err := VerifyStaging(staging, entries); err != nil {
		return nil, nil, nil, err
	}
	managed, err := readManagedHashes(u.Paths)
	if err != nil {
		return nil, nil, nil, err
	}
	var plan []plannedFile
	res := &ApplyResult{}
	for _, e := range entries {
		target, skip, err := u.resolveDest(e.Dest)
		if err != nil {
			return nil, nil, nil, err
		}
		if skip {
			res.Skipped++
			continue
		}
		if strings.HasPrefix(e.Path, "webui/") {
			res.HasWebUI = true
		} else {
			res.HasCore = true
		}
		plan = append(plan, plannedFile{e, target})
	}
	return plan, res, managed, nil
}

type undoOp struct {
	target string
	backup string // "" — файла не было
}

type plannedFile struct {
	entry  ManifestEntry
	target string
}

// Apply проверяет staging по манифесту и раскладывает файлы. До проверки всех
// файлов на диск ничего не пишется. При ошибке заменённые файлы возвращаются
// на место, созданные — удаляются.
func (u *Updater) Apply(staging string, opts ApplyOptions) (*ApplyResult, error) {
	plan, res, managed, err := u.planFiles(staging)
	if err != nil {
		return nil, err
	}
	managedChanged := false

	backupDir, err := os.MkdirTemp(TempDir(), "rollback-")
	if err != nil {
		if mkErr := os.MkdirAll(TempDir(), 0o755); mkErr != nil {
			return nil, mkErr
		}
		if backupDir, err = os.MkdirTemp(TempDir(), "rollback-"); err != nil {
			return nil, err
		}
	}
	cleanupBackup := true
	defer func() {
		if cleanupBackup {
			_ = os.RemoveAll(backupDir)
		}
	}()

	var undo []undoOp
	rollback := func() error {
		var rollbackErrs []error
		for i := len(undo) - 1; i >= 0; i-- {
			op := undo[i]
			if op.backup == "" {
				if err := os.Remove(op.target); err != nil && !errors.Is(err, os.ErrNotExist) {
					rollbackErrs = append(rollbackErrs, fmt.Errorf("удаление %s: %w", op.target, err))
				}
				continue
			}
			data, err := os.ReadFile(op.backup)
			if err != nil {
				rollbackErrs = append(rollbackErrs, fmt.Errorf("чтение снимка %s: %w", op.target, err))
				continue
			}
			if err := fsutil.AtomicWriteFile(op.target, data, 0); err != nil {
				rollbackErrs = append(rollbackErrs, fmt.Errorf("восстановление %s: %w", op.target, err))
			}
		}
		return errors.Join(rollbackErrs...)
	}
	withRollback := func(cause error) error {
		return errors.Join(cause, rollback())
	}

	for i, pl := range plan {
		changed, err := u.applyPlannedFile(staging, backupDir, i, pl, managed, res, &undo)
		if err != nil {
			return nil, withRollback(err)
		}
		managedChanged = managedChanged || changed
	}
	if managedChanged {
		ledger := managedHashesPath(u.Paths)
		op := undoOp{target: ledger}
		if old, err := os.ReadFile(ledger); err == nil {
			op.backup = filepath.Join(backupDir, "managed-resources.tsv")
			if err := os.WriteFile(op.backup, old, 0o600); err != nil {
				return nil, withRollback(err)
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, withRollback(err)
		}
		undo = append(undo, op)
		if err := writeManagedHashes(u.Paths, managed); err != nil {
			return nil, withRollback(err)
		}
	}

	ver, err := u.mergeVersion(staging, res, opts)
	if err != nil {
		return nil, withRollback(err)
	}
	versionTarget := VersionPath(u.Paths)
	versionBackup := undoOp{target: versionTarget}
	if old, err := os.ReadFile(versionTarget); err == nil {
		versionBackup.backup = filepath.Join(backupDir, "version.env")
		if err := os.WriteFile(versionBackup.backup, old, 0o600); err != nil {
			return nil, withRollback(err)
		}
	} else if !os.IsNotExist(err) {
		return nil, withRollback(err)
	}
	undo = append(undo, versionBackup)
	res.Version = ver
	if err := WriteVersion(u.Paths, ver); err != nil {
		return nil, withRollback(err)
	}
	if opts.deferCommit {
		finished := false
		res.finish = func(commit bool) error {
			if finished {
				return nil
			}
			finished = true
			var rollbackErr error
			if !commit {
				rollbackErr = rollback()
			}
			return errors.Join(rollbackErr, os.RemoveAll(backupDir))
		}
		cleanupBackup = false
	}
	return res, nil
}

func (u *Updater) applyPlannedFile(staging, backupDir string, index int, pl plannedFile, managed map[string]string, res *ApplyResult, undo *[]undoOp) (managedChanged bool, err error) {
	e, target := pl.entry, pl.target
	st, statErr := os.Lstat(target)
	exists := statErr == nil
	if exists && st.Mode()&os.ModeSymlink != 0 {
		return false, fmt.Errorf("deploy: %s — символическая ссылка, замена отклонена", target)
	}
	if exists && e.Class == ClassKeepIfExists {
		res.Kept++
		return false, nil
	}
	if e.Class == ClassUpdateIfUnmodified && exists {
		currentSHA, hashErr := fetch.SHA256File(target)
		if hashErr != nil {
			return false, hashErr
		}
		if currentSHA == e.SHA256 {
			res.Unchanged++
			if managed[e.Path] != e.SHA256 {
				managed[e.Path] = e.SHA256
				return true, nil
			}
			return false, nil
		}
		baseline, tracked := managed[e.Path]
		if !tracked || !strings.EqualFold(currentSHA, baseline) {
			res.Kept++
			return false, nil
		}
	}
	if exists && st.Mode().IsRegular() && e.Class != ClassUpdateIfUnmodified {
		if sum, err := fetch.SHA256File(target); err == nil && sum == e.SHA256 {
			res.Unchanged++
			return false, nil
		}
	}
	if exists && !st.Mode().IsRegular() {
		return false, fmt.Errorf("deploy: %s не является обычным файлом", target)
	}
	data, err := os.ReadFile(filepath.Join(staging, filepath.FromSlash(e.Path)))
	if err != nil {
		return false, err
	}
	op := undoOp{target: target}
	if exists {
		op.backup = filepath.Join(backupDir, fmt.Sprintf("%d", index))
		old, err := os.ReadFile(target)
		if err != nil {
			return false, err
		}
		if err := os.WriteFile(op.backup, old, 0o600); err != nil {
			return false, err
		}
	}
	perm := os.FileMode(0o644)
	if e.Exec {
		perm = 0o755
	}
	*undo = append(*undo, op)
	if err := fsutil.AtomicWriteFile(target, data, perm); err != nil {
		return false, fmt.Errorf("deploy: установка %s: %w", target, err)
	}
	if err := os.Chmod(target, perm); err != nil && perm == 0o755 {
		return false, err
	}
	res.Installed++
	res.Files = append(res.Files, target)
	if e.Class == ClassUpdateIfUnmodified {
		managed[e.Path] = e.SHA256
		managedChanged = true
	}
	if u.ScriptDest != "" && target == u.ScriptDest {
		res.ScriptUpdated = true
	}
	return managedChanged, nil
}

// mergeVersion строит итоговую version.env: данные архива заменяют только те
// части (ядро / веб-панель), которые архив содержал.
func (u *Updater) mergeVersion(staging string, res *ApplyResult, opts ApplyOptions) (Version, error) {
	old, err := ReadVersion(u.Paths)
	if err != nil {
		return Version{}, err
	}
	var nv Version
	if data, err := readFileOpt(filepath.Join(staging, filepath.FromSlash(VersionEnvRel))); err != nil {
		return Version{}, err
	} else if data != nil {
		nv = ParseVersionEnv(string(data))
	} else if m := opts.Meta; m != nil {
		nv = Version{
			ZatorVersion: m.Release, ZatorDate: m.ZatorDate, ZatorCommit: m.Commit, ZatorSHA: m.ZatorSHA,
			WebUIVersion: m.Release, WebUIDate: m.WebUIDate, WebUISHA: m.WebUISHA,
		}
	}
	out := old
	if opts.Component == ModeConfig {
		if nv.ConfigVersion != "" {
			out.ConfigVersion = nv.ConfigVersion
		}
		if nv.ConfigSHA != "" {
			out.ConfigSHA = nv.ConfigSHA
		}
		if opts.Meta != nil {
			out.ConfigVersion = opts.Meta.Release
			if opts.Meta.ConfigSHA != "" {
				out.ConfigSHA = opts.Meta.ConfigSHA
			}
		}
	} else if res.HasCore {
		out.ZatorVersion, out.ZatorDate, out.ZatorCommit, out.ZatorSHA = nv.ZatorVersion, nv.ZatorDate, nv.ZatorCommit, nv.ZatorSHA
	}
	if opts.Component != ModeConfig && res.HasWebUI {
		out.WebUIVersion, out.WebUIDate, out.WebUISHA = nv.WebUIVersion, nv.WebUIDate, nv.WebUISHA
	}
	switch {
	case opts.Tracking != "":
		out.Tracking = opts.Tracking
	case out.Tracking == "":
		out.Tracking = TrackingLatest
	}
	return out, nil
}

// ApplyArchive распаковывает архив во временный staging и применяет его.
func (u *Updater) ApplyArchive(archive string, opts ApplyOptions) (*ApplyResult, error) {
	staging, err := u.Unpack(archive)
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(staging)
	return u.Apply(staging, opts)
}

// RunOptions настраивает Run.
type RunOptions struct {
	// Mode selects app (binary+app resources), config (config resources only),
	// or full.
	Mode string
	// Force применяет архив даже при отсутствии обновления.
	Force bool
	// Tracking — значение TRACKING (по умолчанию latest).
	Tracking string
}

// Report — итог Run.
type Report struct {
	Check    *CheckResult
	Variant  string
	Archive  string
	UpToDate bool
	Apply    *ApplyResult
}

// RunMode applies one of the user-facing update modes using independent release
// archives. The app archive and binary are changed together; config-only never
// fetches or replaces the binary.
func (u *Updater) RunMode(ctx context.Context, opts RunOptions) (*Report, error) {
	mode := opts.Mode
	if mode == "" {
		mode = ModeFull
	}
	if mode != ModeApp && mode != ModeConfig && mode != ModeFull {
		return nil, fmt.Errorf("%w: mode %q", ErrUnknownVariant, mode)
	}
	if u.Offline {
		return nil, ErrOffline
	}
	check, err := u.CheckLatest(ctx)
	if err != nil && check == nil {
		return nil, err
	}
	rep := &Report{Check: check, Variant: mode}
	if check == nil || check.Latest == nil {
		return rep, fmt.Errorf("%w: latest.json не содержит release metadata", ErrInvalidLatest)
	}
	doApp := mode == ModeApp || mode == ModeFull && (check.UpdateZator || check.UpdateWebUI)
	doConfig := mode == ModeConfig || mode == ModeFull && check.UpdateConfig
	if opts.Force {
		doApp = mode != ModeConfig
		doConfig = mode != ModeApp
	}
	if !doApp && !doConfig {
		rep.UpToDate = true
		return rep, nil
	}
	if doApp {
		if _, err := check.Latest.Asset(VariantApp); err != nil {
			return rep, err
		}
	}
	if doConfig {
		if _, err := check.Latest.Asset(VariantConfig); err != nil {
			return rep, err
		}
	}
	files := make(map[string]string, 2)
	defer func() {
		for _, file := range files {
			_ = os.Remove(file)
		}
	}()
	var specs []archiveSpec
	if doApp {
		file, err := u.DownloadAsset(ctx, check.Latest, VariantApp)
		if err != nil {
			return rep, err
		}
		files[VariantApp] = file
		specs = append(specs, archiveSpec{file: file, tracking: opts.Tracking, component: ModeApp, meta: check.Latest})
		rep.Archive = VariantApp
	}
	if doConfig {
		file, err := u.DownloadAsset(ctx, check.Latest, VariantConfig)
		if err != nil {
			return rep, err
		}
		files[VariantConfig] = file
		specs = append(specs, archiveSpec{file: file, tracking: opts.Tracking, component: ModeConfig, meta: check.Latest})
		rep.Archive = VariantConfig
	}
	tx, err := u.applyArchives(specs)
	if tx != nil {
		rep.Apply = tx.result
	}
	if err != nil {
		return rep, err
	}
	if doApp {
		var bin *ApplyResult
		var tag string
		if u.applyBinary == nil {
			bin, tag, err = u.applyGitHubBinary(ctx, check.Latest.Release)
		} else {
			bin, tag, err = u.applyBinary(ctx)
		}
		if err != nil {
			return rep, tx.rollback(fmt.Errorf("deploy: установка бинарника: %w", err))
		}
		if tag != "" && CompareVersions(tag, check.Latest.Release) != 0 {
			return rep, tx.rollback(fmt.Errorf("deploy: бинарник %s не совпадает с релизом %s", tag, check.Latest.Release))
		}
		if bin != nil && rep.Apply != nil {
			rep.Apply.Installed += bin.Installed
		}
	}
	if err := tx.finish(true); err != nil {
		return rep, err
	}
	return rep, nil
}

// ApplyRemoteResources downloads and applies resource archives without touching
// the running binary. Used by first install before the app has its final path.
func (u *Updater) ApplyRemoteResources(ctx context.Context, mode string) (*ApplyResult, error) {
	if u.Offline {
		return nil, ErrOffline
	}
	check, err := u.CheckLatest(ctx)
	if err != nil {
		return nil, err
	}
	if check.Latest == nil {
		return nil, fmt.Errorf("%w: latest metadata missing", ErrInvalidLatest)
	}
	packages := map[string]string{}
	for _, name := range []string{VariantApp, VariantConfig} {
		if mode == ModeApp && name == VariantConfig || mode == ModeConfig && name == VariantApp {
			continue
		}
		asset, err := check.Latest.Asset(name)
		if err != nil {
			return nil, err
		}
		archive, err := u.DownloadAsset(ctx, check.Latest, name)
		if err != nil {
			return nil, err
		}
		packages[name] = archive
		_ = asset
	}
	defer func() {
		for _, file := range packages {
			_ = os.Remove(file)
		}
	}()
	return u.applyResourceArchives(mode, packages, check.Latest, TrackingLatest)
}

func (u *Updater) applyResourceArchives(mode string, files map[string]string, meta *Latest, tracking string) (*ApplyResult, error) {
	if mode == "" {
		mode = ModeFull
	}
	if mode != ModeApp && mode != ModeConfig && mode != ModeFull {
		return nil, fmt.Errorf("%w: mode %q", ErrUnknownVariant, mode)
	}
	var specs []archiveSpec
	if mode != ModeConfig {
		file := files[VariantApp]
		if file == "" {
			return nil, fmt.Errorf("deploy: archive %s is missing", VariantApp)
		}
		specs = append(specs, archiveSpec{file: file, tracking: tracking, meta: meta, component: ModeApp})
	}
	if mode != ModeApp {
		file := files[VariantConfig]
		if file == "" {
			return nil, fmt.Errorf("deploy: archive %s is missing", VariantConfig)
		}
		specs = append(specs, archiveSpec{file: file, tracking: tracking, meta: meta, component: ModeConfig})
	}
	tx, err := u.applyArchives(specs)
	if err != nil {
		return tx.result, err
	}
	if err := tx.finish(true); err != nil {
		return tx.result, err
	}
	return tx.result, nil
}
