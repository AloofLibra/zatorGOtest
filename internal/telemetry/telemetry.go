// Package telemetry — необязательная анонимная статистика zator
// (провайдер + номера стратегий) и подсказки стратегий по провайдеру.
//
// Согласие и идентификатор установки хранятся в telemetry.config
// (tel_enabled, tel_uuid, tel_channel_id). Токен сервера статистики никогда
// не хранится в исходниках: он читается из переменной окружения
// ZATOR_STATS_TOKEN; без токена отправка не выполняется (ErrNoToken).
package telemetry

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/AloofLibra/zator/internal/config"
	"github.com/AloofLibra/zator/internal/fsutil"
	"github.com/AloofLibra/zator/internal/orchestra"
	"github.com/AloofLibra/zator/internal/paths"
	"github.com/AloofLibra/zator/internal/platform"
	"github.com/AloofLibra/zator/internal/provider"
)

// Константы протокола и файлов.
const (
	// ConfigFileName — файл согласия (в paths.Paths.ConfigDir).
	ConfigFileName = "telemetry.config"

	// EnvStatsToken — переменная окружения с токеном сервера статистики.
	EnvStatsToken = "ZATOR_STATS_TOKEN"
	// EnvStatsEndpoint переопределяет адрес отправки статистики.
	EnvStatsEndpoint = "ZATOR_STATS_ENDPOINT"
	// EnvRecsURL переопределяет адрес подсказок.
	EnvRecsURL = "Z2R_RECS_URL"

	DefaultEndpoint  = "https://alooflibra.fun/z4r/telemetry"
	DefaultRecsURL   = "https://alooflibra.fun/z4r/recommendations.tsv"
	DefaultChannelID = "z4r-sql-v3-modes"

	// UUIDLength — длина идентификатора установки.
	UUIDLength = 8
	// SendTimeout — таймаут отправки статистики.
	SendTimeout = 10 * time.Second
	// RecsTimeout — таймаут запроса подсказок.
	RecsTimeout = 5 * time.Second
	// RecsCacheTTL — срок, в течение которого результат запроса (успех или
	// ошибка) не перезапрашивается.
	RecsCacheTTL = 24 * time.Hour
	// MaxRecsBytes — максимальный размер файла подсказок.
	MaxRecsBytes = 8192
	// MaxISPLen — максимальная длина названия провайдера в отправке.
	MaxISPLen = 60
)

// Ошибки пакета.
var (
	// ErrDisabled — пользователь не дал согласия на отправку.
	ErrDisabled = errors.New("telemetry: отправка не разрешена")
	// ErrNoToken — не задан токен ZATOR_STATS_TOKEN.
	ErrNoToken = errors.New("telemetry: не задан ZATOR_STATS_TOKEN")
	// ErrNoUUID — у установки нет идентификатора.
	ErrNoUUID = errors.New("telemetry: нет идентификатора установки")
)

// HTTPDoer — минимальный интерфейс HTTP-клиента (подменяется в тестах).
type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

// Settings — содержимое telemetry.config.
type Settings struct {
	// Decided — пользователь уже сделал выбор (tel_enabled присутствует).
	Decided bool
	// Enabled — согласие на отправку (tel_enabled=1).
	Enabled bool
	// UUID — 8-символьный идентификатор установки (tel_uuid).
	UUID string
	// ChannelID — канал телеметрии, под которым установка отправлялась (tel_channel_id).
	ChannelID string
}

// ParseSettings разбирает текст telemetry.config (KEY=VALUE, кавычки допускаются).
func ParseSettings(text string) Settings {
	var s Settings
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(strings.TrimRight(line, "\r"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		v = strings.Trim(strings.TrimSpace(v), `"'`)
		switch strings.TrimSpace(k) {
		case "tel_enabled":
			if v != "" {
				s.Decided = true
				s.Enabled = v == "1"
			}
		case "tel_uuid":
			s.UUID = v
		case "tel_channel_id":
			s.ChannelID = v
		}
	}
	return s
}

// String форматирует настройки в виде telemetry.config. Неопределённое
// согласие не записывается; пустой channel_id опускается.
func (s Settings) String() string {
	var b strings.Builder
	if s.Decided {
		en := "0"
		if s.Enabled {
			en = "1"
		}
		b.WriteString("tel_enabled=" + en + "\n")
	}
	b.WriteString("tel_uuid=" + s.UUID + "\n")
	if s.ChannelID != "" {
		b.WriteString("tel_channel_id=" + s.ChannelID + "\n")
	}
	return b.String()
}

// GenerateUUID возвращает случайный идентификатор из UUIDLength hex-символов.
func GenerateUUID() (string, error) {
	b := make([]byte, UUIDLength/2)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// ValidUUID сообщает, что строка — корректный идентификатор установки.
func ValidUUID(s string) bool {
	if len(s) != UUIDLength {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// Client — клиент телеметрии и подсказок.
type Client struct {
	Paths paths.Paths
	HTTP  HTTPDoer

	// Endpoint — адрес отправки статистики.
	Endpoint string
	// RecsURL — адрес подсказок.
	RecsURL string
	// ChannelID — текущий канал телеметрии.
	ChannelID string
	// Token — токен сервера статистики (по умолчанию из ZATOR_STATS_TOKEN).
	Token string

	// OS возвращает имя платформы для отправки (по умолчанию определяется
	// через internal/platform).
	OS func() string
	// WebUI сообщает, установлен ли веб-интерфейс (по умолчанию —
	// существует ли BinDir/../webui/run-webui.sh или WebUIDir/index.html).
	WebUI func() bool
	// Now — источник времени (по умолчанию time.Now).
	Now func() time.Time

	mu sync.Mutex
}

// New создаёт клиент для раскладки p. Токен берётся из окружения.
func New(p paths.Paths) *Client {
	c := &Client{
		Paths:     p,
		HTTP:      &http.Client{Timeout: SendTimeout},
		Endpoint:  DefaultEndpoint,
		RecsURL:   DefaultRecsURL,
		ChannelID: DefaultChannelID,
		Token:     os.Getenv(EnvStatsToken),
	}
	if v := os.Getenv(EnvStatsEndpoint); v != "" {
		c.Endpoint = v
	}
	if v := os.Getenv(EnvRecsURL); v != "" {
		c.RecsURL = v
	}
	return c
}

func (c *Client) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func (c *Client) doer() HTTPDoer {
	if c.HTTP != nil {
		return c.HTTP
	}
	return http.DefaultClient
}

// ConfigPath возвращает путь к telemetry.config.
func (c *Client) ConfigPath() string { return filepath.Join(c.Paths.ConfigDir, ConfigFileName) }

// LoadSettings читает telemetry.config; отсутствующий файл даёт нулевые настройки.
func (c *Client) LoadSettings() (Settings, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.loadSettings()
}

func (c *Client) loadSettings() (Settings, error) {
	data, err := os.ReadFile(c.ConfigPath())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Settings{}, nil
		}
		return Settings{}, err
	}
	return ParseSettings(string(data)), nil
}

// SaveSettings записывает telemetry.config атомарно.
func (c *Client) SaveSettings(s Settings) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.saveSettings(s)
}

func (c *Client) saveSettings(s Settings) error {
	return fsutil.AtomicWriteFile(c.ConfigPath(), []byte(s.String()), 0o600)
}

// SetEnabled сохраняет выбор пользователя. При включении создаётся UUID,
// если его ещё нет. Возвращает итоговые настройки.
func (c *Client) SetEnabled(enabled bool) (Settings, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	s, err := c.loadSettings()
	if err != nil {
		return s, err
	}
	s.Decided, s.Enabled = true, enabled
	if enabled && !ValidUUID(s.UUID) {
		if s.UUID, err = GenerateUUID(); err != nil {
			return s, err
		}
	}
	return s, c.saveSettings(s)
}

// EnsureUUID возвращает идентификатор установки, создавая и сохраняя его при
// необходимости (8 hex-символов).
func (c *Client) EnsureUUID() (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	s, err := c.loadSettings()
	if err != nil {
		return "", err
	}
	if ValidUUID(s.UUID) {
		return s.UUID, nil
	}
	if s.UUID, err = GenerateUUID(); err != nil {
		return "", err
	}
	return s.UUID, c.saveSettings(s)
}

// Init — аналог init_telemetry: если выбор не сделан, вызывает ask (nil —
// выбор остаётся неопределённым и ничего не отправляется). При включённой
// телеметрии создаёт UUID; если сохранённый канал не совпадает с текущим,
// выполняет первичную отправку и запоминает канал. Ошибка отправки не
// фатальна и возвращается вместе с актуальными настройками.
func (c *Client) Init(ctx context.Context, ask func() bool) (Settings, error) {
	s, err := c.LoadSettings()
	if err != nil {
		return s, err
	}
	if !s.Decided {
		if ask == nil {
			return s, nil
		}
		if s, err = c.SetEnabled(ask()); err != nil {
			return s, err
		}
	}
	if !s.Enabled {
		return s, nil
	}
	if !ValidUUID(s.UUID) {
		if _, err = c.EnsureUUID(); err != nil {
			return s, err
		}
		if s, err = c.LoadSettings(); err != nil {
			return s, err
		}
	}
	if s.ChannelID != c.ChannelID {
		sendErr := c.SendStats(ctx)
		s.ChannelID = c.ChannelID
		if err := c.SaveSettings(s); err != nil {
			return s, err
		}
		return s, sendErr
	}
	return s, nil
}

// ---------------------------------------------------------------------------
// Статистика

// Profile-номера статистики (совпадают с профилями orchestra).
const (
	profTCP    = "1"
	profGV     = "2"
	profRKN    = "3"
	profDS     = "4"
	profUDP    = "5"
	profVoice  = "6"
	profGames  = "7"
	profFBTLS  = "8"
	profFBHTTP = "9"
)

// Stats — снимок, отправляемый на сервер статистики.
type Stats struct {
	UUID string
	ISP  string

	// Номера стратегий по профилям (0 — авто/нет фиксации).
	UDP, TCP, GV, RKN, DSTLS, VoiceUDP, GamesUDP, FBTLS, FBHTTP int

	// TLS blob: глобальный и по профилям 1,2,3,4,8 (пусто — неизвестно).
	BlobGlobal string
	Blob       map[int]string
	// Режим фейков по профилям 1,2,3,4,8 (classic|clone; пусто — модуль недоступен).
	Mode map[int]string

	OS    string
	WebUI bool
}

// overrideProfiles — профили, для которых отправляются blob и режим.
var overrideProfiles = []int{1, 2, 3, 4, 8}

// Values формирует тело POST (без токена).
func (s Stats) Values() url.Values {
	v := url.Values{}
	v.Set("uuid", s.UUID)
	v.Set("isp", s.ISP)
	v.Set("udp", strconv.Itoa(s.UDP))
	v.Set("tcp", strconv.Itoa(s.TCP))
	v.Set("gv", strconv.Itoa(s.GV))
	v.Set("rkn", strconv.Itoa(s.RKN))
	v.Set("ds_tls", strconv.Itoa(s.DSTLS))
	v.Set("voice_udp", strconv.Itoa(s.VoiceUDP))
	v.Set("games_udp", strconv.Itoa(s.GamesUDP))
	v.Set("fb_tls", strconv.Itoa(s.FBTLS))
	v.Set("fb_http", strconv.Itoa(s.FBHTTP))
	v.Set("blob_global", s.BlobGlobal)
	for _, p := range overrideProfiles {
		v.Set("blob_"+strconv.Itoa(p), s.Blob[p])
		v.Set("mode_"+strconv.Itoa(p), s.Mode[p])
	}
	v.Set("os", s.OS)
	if s.WebUI {
		v.Set("webui", "1")
	} else {
		v.Set("webui", "0")
	}
	return v
}

// ISPForSend нормализует название провайдера: до MaxISPLen символов, при
// пустом значении — "Unknown".
func ISPForSend(s string) string {
	s = strings.TrimSpace(strings.NewReplacer("\r", "", "\n", "").Replace(s))
	if r := []rune(s); len(r) > MaxISPLen {
		s = string(r[:MaxISPLen])
	}
	if s == "" {
		return "Unknown"
	}
	return s
}

// CollectStats собирает снимок для отправки: провайдер из provider.txt,
// фиксации стратегий default-scope, blob и режимы фейков.
func (c *Client) CollectStats(uuid string) (Stats, error) {
	st := orchestra.NewStore(c.Paths.StateOrchestraDir())
	s := Stats{
		UUID: uuid,
		Blob: map[int]string{},
		Mode: map[int]string{},
	}
	if isp, ok := provider.ReadCache(c.Paths); ok {
		s.ISP = ISPForSend(isp)
	} else {
		s.ISP = "Unknown"
	}

	lock := func(profile string, proto orchestra.Proto) (int, error) {
		v, err := st.GetLock(orchestra.ScopeDefault, profile, string(proto))
		if err != nil {
			return 0, err
		}
		if v.IsAuto() {
			return 0, nil
		}
		return int(v), nil
	}
	targets := []struct {
		dst     *int
		profile string
		proto   orchestra.Proto
	}{
		{&s.TCP, profTCP, orchestra.ProtoTLS},
		{&s.GV, profGV, orchestra.ProtoTLS},
		{&s.RKN, profRKN, orchestra.ProtoTLS},
		{&s.DSTLS, profDS, orchestra.ProtoTLS},
		{&s.UDP, profUDP, orchestra.ProtoUDP},
		{&s.VoiceUDP, profVoice, orchestra.ProtoUDP},
		{&s.GamesUDP, profGames, orchestra.ProtoUDP},
		{&s.FBTLS, profFBTLS, orchestra.ProtoTLS},
		{&s.FBHTTP, profFBHTTP, orchestra.ProtoHTTP},
	}
	for _, t := range targets {
		n, err := lock(t.profile, t.proto)
		if err != nil {
			return s, fmt.Errorf("telemetry: профиль %s: %w", t.profile, err)
		}
		*t.dst = n
	}

	// Глобальный TLS blob из конфигурации zapret2 (пусто — неизвестно).
	cfgPath := c.Paths.ZapretConfig
	if _, err := os.Stat(cfgPath); err != nil {
		cfgPath = filepath.Join(c.Paths.ZapretRoot, "config.default")
	}
	if cfg, err := config.Load(cfgPath); err == nil {
		g := cfg.TLSBlobMenuValue()
		switch g {
		case "default":
			g = "fake_default_tls"
		case "неизвестно", "":
			g = ""
		}
		s.BlobGlobal = g
	}
	blobs, _ := st.OverrideMap(orchestra.OverrideBlob)
	for _, p := range overrideProfiles {
		v := blobs[strconv.Itoa(p)]
		if v == "" {
			v = s.BlobGlobal
		}
		s.Blob[p] = v
		mode, err := st.GetFakeMode(strconv.Itoa(p))
		if err != nil || mode != orchestra.ModeClone {
			mode = orchestra.ModeClassic
		}
		s.Mode[p] = mode
	}

	s.OS = c.osName()
	s.WebUI = c.webUIInstalled()
	return s, nil
}

func (c *Client) osName() string {
	if c.OS != nil {
		return c.OS()
	}
	pl := platform.Detect()
	switch {
	case pl.Hardware != platform.HardwareNone:
		return string(pl.Hardware)
	case pl.OSID != "":
		return strings.ToLower(pl.OSID)
	case pl.Kind == platform.KindEntware:
		return "entware"
	}
	if runtime.GOOS != "" {
		return runtime.GOOS
	}
	return "unknown"
}

func (c *Client) webUIInstalled() bool {
	if c.WebUI != nil {
		return c.WebUI()
	}
	for _, f := range []string{
		filepath.Join(c.Paths.WebUIDir, "run-webui.sh"),
		filepath.Join(c.Paths.WebUIDir, "index.html"),
	} {
		if st, err := os.Stat(f); err == nil && !st.IsDir() {
			return true
		}
	}
	return false
}

// SendStats отправляет статистику POST-запросом (form-urlencoded, поле token).
// ErrDisabled — нет согласия на отправку; ErrNoToken — не задан токен. В обоих
// случаях запрос не выполняется. Отсутствующий UUID при необходимости
// генерируется и сохраняется.
func (c *Client) SendStats(ctx context.Context) error {
	s, err := c.LoadSettings()
	if err != nil {
		return err
	}
	if !s.Enabled {
		return ErrDisabled
	}
	if strings.TrimSpace(c.Token) == "" {
		return ErrNoToken
	}
	uuid := s.UUID
	if !ValidUUID(uuid) {
		if uuid, err = c.EnsureUUID(); err != nil {
			return err
		}
	}
	stats, err := c.CollectStats(uuid)
	if err != nil {
		return err
	}
	return c.post(ctx, stats)
}

// SendSnapshot отправляет готовый снимок, минуя сбор (для тестов и внешних источников).
func (c *Client) SendSnapshot(ctx context.Context, s Stats) error {
	if strings.TrimSpace(c.Token) == "" {
		return ErrNoToken
	}
	if s.UUID == "" {
		return ErrNoUUID
	}
	return c.post(ctx, s)
}

func (c *Client) post(ctx context.Context, s Stats) error {
	form := s.Values()
	form.Set("token", c.Token)
	endpoint := c.Endpoint
	if endpoint == "" {
		endpoint = DefaultEndpoint
	}
	ctx, cancel := context.WithTimeout(ctx, SendTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", "zator-telemetry/1")
	resp, err := c.doer().Do(req)
	if err != nil {
		return fmt.Errorf("telemetry: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("telemetry: %s: HTTP %d", endpoint, resp.StatusCode)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Подсказки (recommendations.tsv)

// Статусы подсказок.
const (
	StatusReady           = "ready"
	StatusInsufficient    = "insufficient"
	StatusStale           = "stale"
	StatusUnavailable     = "unavailable"
	StatusUnknownProvider = "unknown_provider"
)

// MinSamples — минимум установок для показа подсказок.
const MinSamples = 10

// ErrInvalidRecs — файл подсказок не прошёл проверку.
var ErrInvalidRecs = errors.New("telemetry: некорректный файл подсказок")

// ProfileRec — сводка по профилю (1..4).
type ProfileRec struct {
	Samples          int
	CloneRecommended bool
	ClassicPct       *float64 // nil — "-"
	ClonePct         *float64
}

// StrategyRec — строка рейтинга стратегии.
type StrategyRec struct {
	Profile    int
	Strategy   int
	SuccessPct float64
	Samples    int
	Mode       string // classic | clone | mixed
}

// Recommendations — разобранные подсказки по провайдеру.
type Recommendations struct {
	Provider    string
	Status      string
	Samples     int
	GeneratedAt int64
	Profiles    map[int]ProfileRec
	Strategies  []StrategyRec
}

// Top возвращает рейтинг стратегий профиля в порядке файла.
func (r *Recommendations) Top(profile int) []StrategyRec {
	var out []StrategyRec
	if r == nil {
		return nil
	}
	for _, s := range r.Strategies {
		if s.Profile == profile {
			out = append(out, s)
		}
	}
	return out
}

func parseUint(s string) (int, bool) {
	if s == "" || len(s) > 10 {
		return 0, false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, false
		}
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n > int64(^uint(0)>>1) {
		return 0, false
	}
	return int(n), true
}

func parsePct(s string) (float64, bool) {
	if s == "" {
		return 0, false
	}
	dots := 0
	for _, c := range s {
		switch {
		case c == '.':
			dots++
		case c < '0' || c > '9':
			return 0, false
		}
	}
	if dots > 1 || strings.HasPrefix(s, ".") || strings.HasSuffix(s, ".") {
		return 0, false
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || f > 100 {
		return 0, false
	}
	return f, true
}

// ParseRecommendations разбирает и строго проверяет TSV подсказок (порт
// recommendations_valid): размер ≤ 8192 байт, 5..17 строк, схема
// meta/profile/strategy, провайдер совпадает, только числа.
func ParseRecommendations(data []byte, provider string) (*Recommendations, error) {
	bad := func(format string, a ...any) (*Recommendations, error) {
		return nil, fmt.Errorf("%w: %s", ErrInvalidRecs, fmt.Sprintf(format, a...))
	}
	if len(data) == 0 || len(data) > MaxRecsBytes {
		return bad("размер %d", len(data))
	}
	text := strings.TrimSuffix(string(data), "\n")
	lines := strings.Split(text, "\n")
	if len(lines) < 5 || len(lines) > 17 {
		return bad("строк %d", len(lines))
	}
	rec := &Recommendations{Provider: provider, Profiles: map[int]ProfileRec{}}
	tops := map[int]int{}
	seen := map[[2]int]bool{}
	total := 0
	for i, line := range lines {
		f := strings.Split(strings.TrimRight(line, "\r"), "\t")
		if i == 0 {
			if len(f) != 5 || f[0] != "meta" || f[1] != "1" || f[2] != provider {
				return bad("meta")
			}
			n, ok1 := parseUint(f[3])
			at, ok2 := parseUint(f[4])
			if !ok1 || !ok2 || at <= 0 {
				return bad("meta числа")
			}
			total, rec.Samples, rec.GeneratedAt = n, n, int64(at)
			continue
		}
		switch f[0] {
		case "profile":
			if len(f) != 6 || len(f[1]) != 1 || f[1][0] < '1' || f[1][0] > '4' {
				return bad("profile: %q", line)
			}
			id := int(f[1][0] - '0')
			n, ok := parseUint(f[2])
			if _, dup := rec.Profiles[id]; dup || !ok || n > total || (f[3] != "0" && f[3] != "1") {
				return bad("profile: %q", line)
			}
			pr := ProfileRec{Samples: n, CloneRecommended: f[3] == "1"}
			for k, dst := range []**float64{&pr.ClassicPct, &pr.ClonePct} {
				v := f[4+k]
				if v == "-" {
					continue
				}
				x, ok := parsePct(v)
				if !ok {
					return bad("profile pct: %q", line)
				}
				p := x
				*dst = &p
			}
			if pr.CloneRecommended {
				if total < MinSamples || pr.ClassicPct == nil || pr.ClonePct == nil || *pr.ClonePct <= *pr.ClassicPct {
					return bad("profile clone: %q", line)
				}
			}
			rec.Profiles[id] = pr
		case "strategy":
			if len(f) != 6 || len(f[1]) != 1 || f[1][0] < '1' || f[1][0] > '4' {
				return bad("strategy: %q", line)
			}
			id := int(f[1][0] - '0')
			st, ok1 := parseUint(f[2])
			pct, ok2 := parsePct(f[3])
			n, ok3 := parseUint(f[4])
			if !ok1 || !ok2 || !ok3 || st < 1 || n < 1 || n > total || total < MinSamples ||
				(f[5] != "classic" && f[5] != "clone" && f[5] != "mixed") {
				return bad("strategy: %q", line)
			}
			tops[id]++
			key := [2]int{id, st}
			if tops[id] > 3 || seen[key] {
				return bad("strategy dup/limit: %q", line)
			}
			seen[key] = true
			rec.Strategies = append(rec.Strategies, StrategyRec{Profile: id, Strategy: st, SuccessPct: pct, Samples: n, Mode: f[5]})
		default:
			return bad("неизвестная строка: %q", line)
		}
	}
	for id := 1; id <= 4; id++ {
		if _, ok := rec.Profiles[id]; !ok {
			return bad("нет профиля %d", id)
		}
	}
	rec.Status = StatusReady
	if rec.Samples < MinSamples {
		rec.Status = StatusInsufficient
	}
	return rec, nil
}

// RecsFile возвращает путь к кэшу подсказок.
func (c *Client) RecsFile() string {
	return filepath.Join(provider.CacheDir(c.Paths), "recommendations.tsv")
}

func (c *Client) requestFile() string { return c.RecsFile() + ".request" }

type requestMark struct {
	provider string
	ok       bool
	at       time.Time
}

func (c *Client) readRequest() (requestMark, bool) {
	f, err := os.Open(c.requestFile())
	if err != nil {
		return requestMark{}, false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	var lines []string
	for sc.Scan() && len(lines) < 3 {
		lines = append(lines, strings.TrimRight(sc.Text(), "\r"))
	}
	if len(lines) < 3 {
		return requestMark{}, false
	}
	ts, err := strconv.ParseInt(lines[2], 10, 64)
	if err != nil {
		return requestMark{}, false
	}
	return requestMark{provider: lines[0], ok: lines[1] == "ok", at: time.Unix(ts, 0)}, true
}

func (c *Client) writeRequest(provider string, ok bool) error {
	state := "failed"
	if ok {
		state = "ok"
	}
	body := fmt.Sprintf("%s\n%s\n%d\n", provider, state, c.now().Unix())
	return fsutil.AtomicWriteFile(c.requestFile(), []byte(body), 0)
}

// KnownProvider сообщает, что название провайдера пригодно для запроса подсказок.
func KnownProvider(p string) bool {
	switch strings.TrimSpace(p) {
	case "", provider.Unknown, "Unknown":
		return false
	}
	return true
}

// LoadRecommendations читает кэш подсказок без обращения к сети. Статус:
// unknown_provider (провайдер не определён), unavailable (нет валидного кэша),
// insufficient (<10 установок), stale (последний запрос завершился ошибкой),
// ready.
func (c *Client) LoadRecommendations() (*Recommendations, error) {
	prov, _ := provider.ReadCache(c.Paths)
	rec := &Recommendations{Provider: prov, Status: StatusUnknownProvider, Profiles: map[int]ProfileRec{}}
	if !KnownProvider(prov) {
		return rec, nil
	}
	rec.Status = StatusUnavailable
	data, err := os.ReadFile(c.RecsFile())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return rec, nil
		}
		return rec, err
	}
	parsed, err := ParseRecommendations(data, prov)
	if err != nil {
		return rec, nil
	}
	if mark, ok := c.readRequest(); ok && mark.provider == prov && !mark.ok && parsed.Status == StatusReady {
		parsed.Status = StatusStale
	}
	return parsed, nil
}

// FetchRecommendations обновляет кэш подсказок (не чаще раза в сутки для
// одного провайдера: и успех, и ошибка запоминаются на RecsCacheTTL) и
// возвращает актуальные данные. Сетевые ошибки не возвращаются как error —
// отражаются в Status (stale/unavailable).
func (c *Client) FetchRecommendations(ctx context.Context) (*Recommendations, error) {
	prov, _ := provider.ReadCache(c.Paths)
	if !KnownProvider(prov) {
		return c.LoadRecommendations()
	}
	c.mu.Lock()
	if mark, ok := c.readRequest(); !(ok && mark.provider == prov && c.now().Sub(mark.at) < RecsCacheTTL && c.now().Sub(mark.at) >= 0) {
		c.refreshRecs(ctx, prov)
	}
	c.mu.Unlock()
	return c.LoadRecommendations()
}

// refreshRecs выполняет запрос и записывает кэш и отметку; блокировка c.mu уже взята.
func (c *Client) refreshRecs(ctx context.Context, prov string) {
	data, err := c.downloadRecs(ctx, prov)
	if err == nil {
		if _, perr := ParseRecommendations(data, prov); perr != nil {
			err = perr
		} else if werr := fsutil.AtomicWriteFile(c.RecsFile(), data, 0); werr != nil {
			err = werr
		}
	}
	_ = c.writeRequest(prov, err == nil)
}

func (c *Client) downloadRecs(ctx context.Context, prov string) ([]byte, error) {
	base := c.RecsURL
	if base == "" {
		base = DefaultRecsURL
	}
	u, err := url.Parse(base)
	if err != nil {
		return nil, err
	}
	q := u.Query()
	q.Set("provider", prov)
	u.RawQuery = q.Encode()

	ctx, cancel := context.WithTimeout(ctx, RecsTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "zator-telemetry/1")
	resp, err := c.doer().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("telemetry: %s: HTTP %d", base, resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, MaxRecsBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > MaxRecsBytes {
		return nil, fmt.Errorf("%w: больше %d байт", ErrInvalidRecs, MaxRecsBytes)
	}
	return data, nil
}
