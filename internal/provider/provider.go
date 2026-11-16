// Package provider определяет интернет-провайдера пользователя по номеру
// автономной системы (ASN) через каскад гео-сервисов
// ipwho.is → ipinfo.io → ip-api.com и кэширует результат в provider.txt.
//
// ASN стабилен и одинаков у всех гео-сервисов, в отличие от поля isp
// (юридические лица и локальные бренды), поэтому бренд берётся из таблицы
// ASN → бренд (встроенная + assets/data/providers/asn.txt + удалённый кэш).
package provider

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/AloofLibra/zator/internal/fsutil"
	"github.com/AloofLibra/zator/internal/paths"
)

// Файлы кэша и источники по умолчанию.
const (
	// CacheFileName — кэш определённого провайдера (первая строка).
	CacheFileName = "provider.txt"
	// ASNCacheFileName — кэш удалённой таблицы ASN.
	ASNCacheFileName = "provider_asn.txt"

	DefaultIPWhoURL  = "https://ipwho.is/"
	DefaultIPInfoURL = "https://ipinfo.io/json"
	DefaultIPAPIURL  = "http://ip-api.com/json/?fields=isp,city,as"
	// DefaultASNRemoteURL — удалённая таблица ASN (opt-in; по умолчанию
	// используется встроенный assets/data/providers/asn.txt без сети).
	DefaultASNRemoteURL = ""
	// LegacyASNRemoteURL — прежний raw.githubusercontent.com feed.
	LegacyASNRemoteURL = "https://raw.githubusercontent.com/AloofLibra/zator/zator/data/providers/asn.txt"

	// DefaultASNTTL — срок жизни кэша таблицы ASN.
	DefaultASNTTL = 7 * 24 * time.Hour
	// RequestTimeout — таймаут одного запроса к гео-сервису.
	RequestTimeout = 6 * time.Second
	// MaxFieldLen — предельная длина очищенного поля (isp, city) в символах.
	MaxFieldLen = 60

	// Unknown — текст, когда провайдер не определён.
	Unknown = "Не определён"

	maxBody = 1 << 20
)

// Ошибки пакета.
var (
	// ErrNotDetected — ни один сервис не вернул пригодных данных.
	ErrNotDetected = errors.New("provider: не удалось определить провайдера")
	// ErrEmpty — пустое название при ручной установке.
	ErrEmpty = errors.New("provider: пустое название провайдера")
	// ErrInvalidDB — таблица ASN не прошла проверку.
	ErrInvalidDB = errors.New("provider: некорректная таблица ASN")
)

// HTTPDoer — минимальный интерфейс HTTP-клиента (подменяется в тестах).
type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

// CacheDir возвращает каталог кэша провайдера для раскладки p.
func CacheDir(_ paths.Paths) string { return filepath.Join(os.TempDir(), "zator", "cache") }

// CachePath возвращает путь к provider.txt.
func CachePath(p paths.Paths) string { return filepath.Join(CacheDir(p), CacheFileName) }

// ReadCache возвращает первую строку provider.txt раскладки p ("" и false,
// если файла нет или он пуст).
func ReadCache(p paths.Paths) (string, bool) { return readFirstLine(CachePath(p)) }

func readFirstLine(path string) (string, bool) {
	f, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	if sc.Scan() {
		if s := strings.TrimSpace(strings.TrimRight(sc.Text(), "\r")); s != "" {
			return s, true
		}
	}
	return "", false
}

// Result — результат определения провайдера.
type Result struct {
	ASN     int    // номер автономной системы (0 — неизвестен)
	ISP     string // исходное название от гео-сервиса
	City    string
	Brand   string // бренд из таблицы ASN либо ISP (AS…)
	Display string // строка для provider.txt: "Brand" или "Brand - City"
	Source  string // ipwho.is | ipinfo.io | ip-api.com
}

// Detector определяет и кэширует провайдера. Нулевые поля URL заменяются
// значениями по умолчанию; HTTP по умолчанию — клиент с таймаутами.
type Detector struct {
	Paths paths.Paths
	HTTP  HTTPDoer

	IPWhoURL  string
	IPInfoURL string
	IPAPIURL  string

	// ASNDBFile — файл таблицы ASN (по умолчанию DataDir/providers/asn.txt).
	ASNDBFile string
	// ASNCacheFile — кэш удалённой таблицы (по умолчанию CacheDir/provider_asn.txt).
	ASNCacheFile string
	// ASNRemoteURL — источник обновления таблицы; пусто — обновление отключено
	// (по умолчанию DefaultASNRemoteURL).
	ASNRemoteURL string
	// ASNTTL — срок жизни кэша таблицы (по умолчанию DefaultASNTTL).
	ASNTTL time.Duration
	// Now — источник времени (по умолчанию time.Now).
	Now func() time.Time

	mu sync.Mutex
}

// New создаёт детектор с настройками по умолчанию для раскладки p.
func New(p paths.Paths) *Detector {
	return &Detector{
		Paths:        p,
		HTTP:         &http.Client{Timeout: 2 * RequestTimeout},
		IPWhoURL:     DefaultIPWhoURL,
		IPInfoURL:    DefaultIPInfoURL,
		IPAPIURL:     DefaultIPAPIURL,
		ASNDBFile:    filepath.Join(p.DataDir, "providers", "asn.txt"),
		ASNCacheFile: filepath.Join(CacheDir(p), ASNCacheFileName),
		ASNRemoteURL: DefaultASNRemoteURL,
		ASNTTL:       DefaultASNTTL,
	}
}

func (d *Detector) now() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now()
}

func (d *Detector) doer() HTTPDoer {
	if d.HTTP != nil {
		return d.HTTP
	}
	return http.DefaultClient
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

// CacheFile возвращает путь к provider.txt.
func (d *Detector) CacheFile() string { return CachePath(d.Paths) }

// Get возвращает закэшированного провайдера.
func (d *Detector) Get() (string, bool) { return readFirstLine(d.CacheFile()) }

// Menu возвращает закэшированного провайдера либо Unknown.
func (d *Detector) Menu() string {
	if s, ok := d.Get(); ok {
		return s
	}
	return Unknown
}

func (d *Detector) writeCache(s string) error {
	return fsutil.AtomicWriteFile(d.CacheFile(), []byte(s+"\n"), 0)
}

// Set задаёт провайдера вручную: "provider" либо "provider - city".
// Возвращает записанную строку.
func (d *Detector) Set(name, city string) (string, error) {
	name, city = Clean(name), Clean(city)
	if name == "" {
		return "", ErrEmpty
	}
	res := name
	if city != "" {
		res = name + " - " + city
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.writeCache(res); err != nil {
		return "", err
	}
	return res, nil
}

// Clear удаляет кэш провайдера.
func (d *Detector) Clear() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := os.Remove(d.CacheFile()); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// Init возвращает кэш, а при его отсутствии запускает определение.
// Ошибка определения не фатальна: возвращается Unknown и ошибка.
func (d *Detector) Init(ctx context.Context) (string, error) {
	if s, ok := d.Get(); ok {
		return s, nil
	}
	res, err := d.Detect(ctx)
	if err != nil {
		return Unknown, err
	}
	return res.Display, nil
}

// Redetect обновляет таблицу ASN (ошибки игнорируются), сбрасывает кэш и
// определяет провайдера заново.
func (d *Detector) Redetect(ctx context.Context) (Result, error) {
	_ = d.UpdateDatabase(ctx)
	if err := d.Clear(); err != nil {
		return Result{}, err
	}
	return d.Detect(ctx)
}

var (
	spacesRe = regexp.MustCompile(`[\r\n\t]+`)
	asRe     = regexp.MustCompile(`^AS(\d+)\s*(.*)$`)
)

// Clean удаляет \r\n\t, обрезает пробелы и ограничивает длину MaxFieldLen символов.
func Clean(s string) string {
	s = strings.TrimSpace(spacesRe.ReplaceAllString(s, ""))
	if r := []rune(s); len(r) > MaxFieldLen {
		s = strings.TrimSpace(string(r[:MaxFieldLen]))
	}
	return s
}

// geoResp — общая раскладка ответов трёх сервисов.
type geoResp struct {
	data map[string]any
}

func (g geoResp) str(keys ...string) string {
	var cur any = g.data
	for _, k := range keys {
		m, ok := cur.(map[string]any)
		if !ok {
			return ""
		}
		cur = m[k]
	}
	s, _ := cur.(string)
	return s
}

func (g geoResp) num(keys ...string) int {
	var cur any = g.data
	for _, k := range keys {
		m, ok := cur.(map[string]any)
		if !ok {
			return 0
		}
		cur = m[k]
	}
	switch v := cur.(type) {
	case float64:
		if v > 0 && v < 1<<32 {
			return int(v)
		}
	case string:
		if n, err := strconv.Atoi(strings.TrimPrefix(strings.ToUpper(strings.TrimSpace(v)), "AS")); err == nil && n > 0 {
			return n
		}
	}
	return 0
}

func (d *Detector) getJSON(ctx context.Context, rawURL string) (geoResp, error) {
	ctx, cancel := context.WithTimeout(ctx, RequestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return geoResp{}, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "zator-provider/1")
	resp, err := d.doer().Do(req)
	if err != nil {
		return geoResp{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return geoResp{}, fmt.Errorf("provider: %s: HTTP %d", rawURL, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return geoResp{}, err
	}
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		return geoResp{}, fmt.Errorf("provider: %s: %w", rawURL, err)
	}
	return geoResp{data: m}, nil
}

// Detect определяет провайдера каскадом сервисов и записывает кэш.
// Следующий сервис опрашивается, только пока ASN не получен; ошибки сервисов
// не фатальны. ErrNotDetected — если нет ни ASN, ни названия.
func (d *Detector) Detect(ctx context.Context) (Result, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	table, _ := d.loadTable()

	var asn int
	var isp, city, source string

	// 1. ipwho.is (HTTPS, без ключа).
	if g, err := d.getJSON(ctx, orDefault(d.IPWhoURL, DefaultIPWhoURL)); err == nil {
		if ok, _ := g.data["success"].(bool); ok {
			asn = g.num("connection", "asn")
			if asn == 0 {
				asn = g.num("asn")
			}
			isp = Clean(firstNonEmpty(g.str("connection", "isp"), g.str("isp")))
			city = Clean(g.str("city"))
			if asn != 0 {
				source = "ipwho.is"
			}
		}
	}

	// 2. ipinfo.io: org = "AS8359 MTS PJSC".
	if asn == 0 {
		if g, err := d.getJSON(ctx, orDefault(d.IPInfoURL, DefaultIPInfoURL)); err == nil {
			if m := asRe.FindStringSubmatch(strings.TrimSpace(g.str("org"))); m != nil {
				asn, _ = strconv.Atoi(m[1])
				isp = Clean(m[2])
				source = "ipinfo.io"
			}
			if city == "" {
				city = Clean(g.str("city"))
			}
		}
	}

	// 3. ip-api.com (HTTP, последний): as = "AS8359 PJSC ...".
	if asn == 0 {
		if g, err := d.getJSON(ctx, orDefault(d.IPAPIURL, DefaultIPAPIURL)); err == nil {
			if m := asRe.FindStringSubmatch(strings.TrimSpace(g.str("as"))); m != nil {
				asn, _ = strconv.Atoi(m[1])
				source = "ip-api.com"
			}
			if isp == "" {
				isp = Clean(g.str("isp"))
			}
			if city == "" {
				city = Clean(g.str("city"))
			}
			if source == "" && isp != "" {
				source = "ip-api.com"
			}
		}
	}

	var brand string
	switch {
	case asn != 0 && table.has(asn):
		brand, _ = table.Lookup(asn)
	case isp != "":
		brand = isp
		if asn != 0 {
			brand += " (AS" + strconv.Itoa(asn) + ")"
		}
	case asn != 0:
		brand = "AS" + strconv.Itoa(asn)
	default:
		return Result{}, ErrNotDetected
	}

	display := brand
	if city != "" && city != brand {
		display = brand + " - " + city
	}
	if err := d.writeCache(display); err != nil {
		return Result{}, err
	}
	return Result{ASN: asn, ISP: isp, City: city, Brand: brand, Display: display, Source: source}, nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// ---------------------------------------------------------------------------
// Таблица ASN → бренд

// Source — откуда загружена таблица ASN.
type Source string

const (
	SourceBuiltin Source = "builtin"
	SourceFile    Source = "file"
	SourceCache   Source = "cache"
)

// builtinASN — минимальная встроенная база (fallback при отсутствии файлов).
const builtinASN = `8359:MTS:
31133:MTS:MGTS
12389:Rostelecom:
42610:Rostelecom:
20485:Rostelecom:TTK
3216:Beeline:VimpelCom
8369:Dom.ru:ER-Telecom
9049:Dom.ru:ER-Telecom
47119:Ufanet:
25159:MegaFon:
12958:T2:
9002:RETN:
8492:OBIT:
9198:Kazakhtelecom:
15895:Kyivstar:
6697:Beltelecom:`

// minDBRows — минимальное число строк корректной таблицы.
const minDBRows = 5

// Entry — запись таблицы ASN.
type Entry struct {
	ASN     int
	Brand   string
	Aliases []string
}

// Table — таблица ASN → бренд с сохранением порядка.
type Table struct {
	Source  Source
	entries []Entry
	byASN   map[int]int
}

func (t *Table) has(asn int) bool {
	if t == nil {
		return false
	}
	_, ok := t.byASN[asn]
	return ok
}

// Len возвращает число записей.
func (t *Table) Len() int {
	if t == nil {
		return 0
	}
	return len(t.entries)
}

// Entries возвращает копию записей в порядке следования.
func (t *Table) Entries() []Entry {
	if t == nil {
		return nil
	}
	return append([]Entry(nil), t.entries...)
}

// Lookup возвращает бренд ASN.
func (t *Table) Lookup(asn int) (brand string, ok bool) {
	e, ok := t.Entry(asn)
	return e.Brand, ok
}

// Entry возвращает запись ASN.
func (t *Table) Entry(asn int) (Entry, bool) {
	if t == nil {
		return Entry{}, false
	}
	i, ok := t.byASN[asn]
	if !ok {
		return Entry{}, false
	}
	return t.entries[i], true
}

// BrandAliases возвращает уникальные отсортированные алиасы бренда
// (используются для поиска подсказок).
func (t *Table) BrandAliases(brand string) []string {
	if t == nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, e := range t.entries {
		if e.Brand != brand {
			continue
		}
		for _, a := range e.Aliases {
			if a != "" && !seen[a] {
				seen[a] = true
				out = append(out, a)
			}
		}
	}
	sort.Strings(out)
	return out
}

var dbLineRe = regexp.MustCompile(`^(\d+):([^:]+)(?::([^:]*))?\s*$`)

// parseRows разбирает строки формата ASN:BRAND:ALIASES. strict=true отвергает
// любую некорректную строку; иначе такие строки пропускаются.
func parseRows(text string, strict bool) (rows []Entry, err error) {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimRight(line, "\r")
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		m := dbLineRe.FindStringSubmatch(line)
		if m == nil {
			if strict {
				return nil, fmt.Errorf("%w: %q", ErrInvalidDB, line)
			}
			continue
		}
		asn, perr := strconv.Atoi(m[1])
		if perr != nil || asn <= 0 {
			if strict {
				return nil, fmt.Errorf("%w: %q", ErrInvalidDB, line)
			}
			continue
		}
		e := Entry{ASN: asn, Brand: strings.TrimSpace(m[2])}
		for _, a := range strings.Split(m[3], ",") {
			if a = strings.TrimSpace(a); a != "" {
				e.Aliases = append(e.Aliases, a)
			}
		}
		rows = append(rows, e)
	}
	return rows, nil
}

// ParseDatabase разбирает и проверяет таблицу ASN: все непустые строки
// (кроме комментариев) обязаны иметь вид ASN:Бренд[:алиасы], строк не менее 5.
func ParseDatabase(text string) ([]Entry, error) {
	rows, err := parseRows(text, true)
	if err != nil {
		return nil, err
	}
	if len(rows) < minDBRows {
		return nil, fmt.Errorf("%w: строк %d, нужно не менее %d", ErrInvalidDB, len(rows), minDBRows)
	}
	return rows, nil
}

func buildTable(src Source, parts ...[]Entry) *Table {
	t := &Table{Source: src, byASN: map[int]int{}}
	for _, rows := range parts {
		for _, e := range rows {
			if _, dup := t.byASN[e.ASN]; dup {
				continue
			}
			t.byASN[e.ASN] = len(t.entries)
			t.entries = append(t.entries, e)
		}
	}
	return t
}

// BuiltinTable возвращает встроенную таблицу.
func BuiltinTable() *Table {
	rows, _ := parseRows(builtinASN, false)
	return buildTable(SourceBuiltin, rows)
}

func loadValid(path string) ([]Entry, bool) {
	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 {
		return nil, false
	}
	rows, err := ParseDatabase(string(data))
	if err != nil {
		return nil, false
	}
	return rows, true
}

func (d *Detector) asnCacheFile() string {
	return orDefault(d.ASNCacheFile, filepath.Join(CacheDir(d.Paths), ASNCacheFileName))
}

func (d *Detector) asnDBFile() string {
	return orDefault(d.ASNDBFile, filepath.Join(d.Paths.DataDir, "providers", "asn.txt"))
}

// loadTable загружает таблицу: кэш, затем файл assets/data/providers/asn.txt,
// иначе встроенную; к найденной примешивается встроенная (внешняя приоритетнее).
func (d *Detector) loadTable() (*Table, Source) {
	builtin, _ := parseRows(builtinASN, false)
	if rows, ok := loadValid(d.asnCacheFile()); ok {
		return buildTable(SourceCache, rows, builtin), SourceCache
	}
	if rows, ok := loadValid(d.asnDBFile()); ok {
		return buildTable(SourceFile, rows, builtin), SourceFile
	}
	return buildTable(SourceBuiltin, builtin), SourceBuiltin
}

// LoadTable загружает таблицу ASN и сообщает её источник.
func (d *Detector) LoadTable() (*Table, Source) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.loadTable()
}

// Lookup ищет бренд ASN в загруженной таблице.
func (d *Detector) Lookup(asn int) (brand string, aliases []string, ok bool) {
	t, _ := d.LoadTable()
	e, ok := t.Entry(asn)
	return e.Brand, e.Aliases, ok
}

// UpdateDatabase скачивает удалённую таблицу ASN в кэш, если кэш старше ASNTTL
// (или отсутствует). Невалидная таблица отвергается. Пустой ASNRemoteURL —
// обновление отключено (возвращает nil).
func (d *Detector) UpdateDatabase(ctx context.Context) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	remote := d.ASNRemoteURL
	if remote == "" {
		return nil
	}
	cache := d.asnCacheFile()
	ttl := d.ASNTTL
	if ttl <= 0 {
		ttl = DefaultASNTTL
	}
	if st, err := os.Stat(cache); err == nil && d.now().Sub(st.ModTime()) < ttl {
		return nil
	}

	if _, err := url.Parse(remote); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 2*RequestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, remote, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "zator-provider/1")
	resp, err := d.doer().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("provider: %s: HTTP %d", remote, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return err
	}
	if _, err := ParseDatabase(string(body)); err != nil {
		return err
	}
	return fsutil.AtomicWriteFile(cache, body, 0)
}
