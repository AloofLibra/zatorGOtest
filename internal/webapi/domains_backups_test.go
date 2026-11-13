package webapi

import (
	"archive/tar"
	"bytes"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func domainItems(t *testing.T, e *env, list string) []map[string]any {
	t.Helper()
	resp, out := e.get("/cgi-bin/domains.cgi?list=" + list)
	wantStatus(t, resp, http.StatusOK)
	var items []map[string]any
	for _, it := range out["items"].([]any) {
		items = append(items, it.(map[string]any))
	}
	return items
}

func TestDomainsNetrogat(t *testing.T) {
	e := newEnv(t, envOpts{})
	resp, out := e.post("/cgi-bin/domains.cgi", "list", "netrogat", "action", "clear")
	wantOK(t, resp, out)

	resp, out = e.get("/cgi-bin/domains.cgi?list=netrogat")
	wantStatus(t, resp, 200)
	if out["kind"] != "domain" || out["is_custom_rkn"] != false || out["max_strategy"] != float64(0) {
		t.Errorf("meta = %v", out)
	}

	resp, out = e.post("/cgi-bin/domains.cgi", "list", "netrogat", "action", "add", "domain", "HTTPS://Pinterest.com/path")
	wantOK(t, resp, out)
	if out["duplicate"] != false {
		t.Errorf("duplicate = %v", out["duplicate"])
	}
	resp, out = e.post("/cgi-bin/domains.cgi", "list", "netrogat", "action", "add", "domain", "pinterest.com")
	wantOK(t, resp, out)
	if out["duplicate"] != true {
		t.Errorf("duplicate = %v", out["duplicate"])
	}
	if items := domainItems(t, e, "netrogat"); len(items) != 1 || items[0]["value"] != "pinterest.com" {
		t.Errorf("items = %v", items)
	}

	resp, out = e.post("/cgi-bin/domains.cgi", "list", "netrogat", "action", "rename", "domain", "pinterest.com", "new_domain", "avito.ru")
	wantOK(t, resp, out)
	if out["domain"] != "avito.ru" {
		t.Errorf("rename = %v", out)
	}
	resp, out = e.post("/cgi-bin/domains.cgi", "list", "netrogat", "action", "rename", "domain", "nope.ru", "new_domain", "a.ru")
	wantError(t, resp, out, 400, "Записи нет в списке")

	resp, out = e.post("/cgi-bin/domains.cgi", "list", "netrogat", "action", "import", "domain", "a.com\nb.com, a.com\nbad_domain\n#c\nc.org")
	wantOK(t, resp, out)
	if out["added"] != float64(3) || out["duplicates"] != float64(1) || out["skipped"] != float64(1) {
		t.Errorf("import = %v", out)
	}
	resp, out = e.post("/cgi-bin/domains.cgi", "list", "netrogat", "action", "rename", "domain", "a.com", "new_domain", "b.com")
	wantError(t, resp, out, 409, "Запись b.com уже есть в списке")

	resp, out = e.post("/cgi-bin/domains.cgi", "list", "netrogat", "action", "remove", "domain", "a.com")
	wantOK(t, resp, out)
	resp, out = e.post("/cgi-bin/domains.cgi", "list", "netrogat", "action", "clear")
	wantOK(t, resp, out)
	if out["cleared"] != float64(3) {
		t.Errorf("clear = %v", out)
	}
	if len(domainItems(t, e, "netrogat")) != 0 {
		t.Error("список не очищен")
	}

	// Ошибки.
	resp, out = e.get("/cgi-bin/domains.cgi?list=zzz")
	wantError(t, resp, out, 400, "Неизвестный список")
	resp, out = e.post("/cgi-bin/domains.cgi", "list", "netrogat", "action", "add")
	wantError(t, resp, out, 400, "Не указан домен")
	resp, out = e.post("/cgi-bin/domains.cgi", "list", "netrogat", "action", "add", "domain", "not a domain")
	wantError(t, resp, out, 400, "Некорректный домен: not a domain")
	resp, out = e.post("/cgi-bin/domains.cgi", "list", "netrogat", "action", "boom")
	wantError(t, resp, out, 400, "Неизвестное действие: boom")
	resp, out = e.json(http.MethodDelete, "/cgi-bin/domains.cgi?list=netrogat", nil)
	wantError(t, resp, out, 405, "Метод не поддерживается")
}

func TestDomainsSubstring(t *testing.T) {
	e := newEnv(t, envOpts{})
	resp, out := e.post("/cgi-bin/domains.cgi", "list", "substring", "action", "add", "domain", "Tube")
	wantOK(t, resp, out)
	if items := domainItems(t, e, "substring"); len(items) != 1 || items[0]["value"] != "Tube" {
		t.Errorf("items = %v", items) // подстроки не нормализуются
	}
	resp, out = e.post("/cgi-bin/domains.cgi", "list", "substring", "action", "add", "domain", "  ")
	wantError(t, resp, out, 400, "Пустая подстрока")
	resp, out = e.post("/cgi-bin/domains.cgi", "list", "netrogat_substring", "action", "add", "domain", "cdn")
	wantOK(t, resp, out)

	// hostlist=авто блокирует ручное пополнение RKN-списков, но не исключений.
	e.post("/cgi-bin/settings.cgi", "setting", "hostlist_state", "value", "1")
	resp, out = e.post("/cgi-bin/domains.cgi", "list", "substring", "action", "add", "domain", "xx")
	wantError(t, resp, out, 409, autoHostlistMsg)
	resp, out = e.post("/cgi-bin/domains.cgi", "list", "custom_rkn", "action", "import", "domain", "a.com")
	wantError(t, resp, out, 409, autoHostlistMsg)
	resp, out = e.post("/cgi-bin/domains.cgi", "list", "netrogat", "action", "add", "domain", "ok.com")
	wantOK(t, resp, out)
}

func TestDomainsCustomRKN(t *testing.T) {
	e := newEnv(t, envOpts{})
	resp, out := e.post("/cgi-bin/domains.cgi", "list", "custom_rkn", "action", "add", "domain", "meduza.io")
	wantOK(t, resp, out)
	chk, ok := out["check"].(map[string]any)
	if !ok || len(chk["results"].([]any)) != 1 {
		t.Fatalf("check = %v", out["check"])
	}
	if len(e.checker.targets) != 1 || e.checker.targets[0] != "https://meduza.io/" {
		t.Errorf("targets = %v", e.checker.targets)
	}

	resp, out = e.post("/cgi-bin/domains.cgi", "list", "custom_rkn", "action", "set_strategy", "domain", "meduza.io", "strategy", "5")
	wantOK(t, resp, out)
	if out["strategy"] != float64(5) || out["check"] == nil {
		t.Errorf("set_strategy = %v", out)
	}
	items := domainItems(t, e, "custom_rkn")
	if len(items) != 1 || items[0]["strategy"] != float64(5) {
		t.Errorf("items = %v", items)
	}
	_, meta := e.get("/cgi-bin/domains.cgi?list=custom_rkn")
	if meta["max_strategy"] != float64(44) || meta["is_custom_rkn"] != true {
		t.Errorf("meta = %v", meta)
	}
	if data, _ := os.ReadFile(e.app.Paths.LockedTSV); !strings.Contains(string(data), "meduza.io") {
		t.Errorf("locked.tsv = %q", data)
	}

	resp, out = e.post("/cgi-bin/domains.cgi", "list", "custom_rkn", "action", "set_strategy", "domain", "meduza.io", "strategy", "45")
	wantError(t, resp, out, 400, "Стратегия вне диапазона (1..44)")
	resp, out = e.post("/cgi-bin/domains.cgi", "list", "custom_rkn", "action", "set_strategy", "domain", "meduza.io", "strategy", "0")
	wantError(t, resp, out, 400, "Стратегия вне диапазона (1..44)")
	resp, out = e.post("/cgi-bin/domains.cgi", "list", "custom_rkn", "action", "set_strategy", "domain", "meduza.io", "strategy", "x")
	wantError(t, resp, out, 400, "Некорректная стратегия")
	resp, out = e.post("/cgi-bin/domains.cgi", "list", "custom_rkn", "action", "set_strategy", "domain", "other.io", "strategy", "1")
	wantError(t, resp, out, 400, "Домена нет в списке")
	resp, out = e.post("/cgi-bin/domains.cgi", "list", "netrogat", "action", "set_strategy", "domain", "a.com", "strategy", "1")
	wantError(t, resp, out, 400, "Стратегия применяется только к TCP_Custom")
	resp, out = e.post("/cgi-bin/domains.cgi", "list", "netrogat", "action", "check", "domain", "a.com")
	wantError(t, resp, out, 400, "Проверка применяется только к TCP_Custom")

	// rename переносит лок.
	resp, out = e.post("/cgi-bin/domains.cgi", "list", "custom_rkn", "action", "rename", "domain", "meduza.io", "new_domain", "dw.com")
	wantOK(t, resp, out)
	items = domainItems(t, e, "custom_rkn")
	if len(items) != 1 || items[0]["value"] != "dw.com" || items[0]["strategy"] != float64(5) {
		t.Errorf("после rename: %v", items)
	}

	resp, out = e.post("/cgi-bin/domains.cgi", "list", "custom_rkn", "action", "check", "domain", "dw.com")
	wantOK(t, resp, out)
	resp, out = e.post("/cgi-bin/domains.cgi", "list", "custom_rkn", "action", "clear_strategy", "domain", "dw.com")
	wantOK(t, resp, out)
	if items = domainItems(t, e, "custom_rkn"); items[0]["strategy"] != float64(0) {
		t.Errorf("clear_strategy: %v", items)
	}

	// Самолечение: домен, оставшийся только в locked.tsv, возвращается в список.
	if err := e.srv.Store().SetLock("default", "lost.example.org", "tls", 3); err != nil {
		t.Fatal(err)
	}
	items = domainItems(t, e, "custom_rkn")
	if len(items) != 2 || items[1]["value"] != "lost.example.org" || items[1]["strategy"] != float64(3) {
		t.Errorf("restore: %v", items)
	}

	// remove чистит локи.
	resp, out = e.post("/cgi-bin/domains.cgi", "list", "custom_rkn", "action", "remove", "domain", "lost.example.org")
	wantOK(t, resp, out)
	if st, _ := e.srv.Store().GetLock("default", "lost.example.org", "tls"); !st.IsAuto() {
		t.Errorf("лок остался: %v", st)
	}
}

func buildTar(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for name, body := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	tw.Close()
	return buf.Bytes()
}

func TestBackups(t *testing.T) {
	e := newEnv(t, envOpts{})
	// Что-нибудь для списков.
	e.post("/cgi-bin/domains.cgi", "list", "netrogat", "action", "add", "domain", "avito.ru")

	resp, out := e.get("/cgi-bin/backups.cgi")
	wantStatus(t, resp, 200)
	if len(out["items"].([]any)) != 0 {
		t.Errorf("items = %v", out["items"])
	}

	resp, out = e.post("/cgi-bin/backups.cgi", "action", "create")
	wantOK(t, resp, out)
	name := out["name"].(string)
	if !backupNameRe.MatchString(name) {
		t.Fatalf("name = %q", name)
	}
	_, out = e.get("/cgi-bin/backups.cgi")
	items := out["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["name"] != name || items[0].(map[string]any)["date"] == "" {
		t.Errorf("items = %v", items)
	}

	// Скачивание: tar с config и lists/.
	resp2, data := e.do(http.MethodGet, "/cgi-bin/backups.cgi?action=download&name="+name, nil, nil)
	if resp2.StatusCode != 200 || resp2.Header.Get("Content-Type") != "application/x-tar" ||
		!strings.Contains(resp2.Header.Get("Content-Disposition"), name) {
		t.Fatalf("download: %d %v", resp2.StatusCode, resp2.Header)
	}
	tr := tar.NewReader(bytes.NewReader(data))
	seen := map[string]bool{}
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		seen[h.Name] = true
	}
	if !seen["config"] || !seen["lists/netrogat.txt"] {
		t.Errorf("архив: %v", seen)
	}

	resp, out = e.get("/cgi-bin/backups.cgi?action=download&name=../../etc/passwd")
	wantError(t, resp, out, 404, "Бэкап не найден")
	resp, out = e.get("/cgi-bin/backups.cgi?action=download")
	wantError(t, resp, out, 400, "Не указано имя файла")

	// Upload.
	good := buildTar(t, map[string]string{"config": "X=1\n"})
	resp3, body := e.do(http.MethodPost, "/cgi-bin/backups.cgi?action=upload&name=z2r_backup_20200101_000000.tar",
		nil, nil)
	_ = body
	wantError2(t, resp3, body, 400, "Пустой файл")

	req, _ := http.NewRequest(http.MethodPost, e.ts.URL+"/cgi-bin/backups.cgi?action=upload&name=z2r_backup_20200101_000000.tar", bytes.NewReader(good))
	req.Header.Set("Content-Type", "application/x-tar")
	r, err := e.ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(r.Body)
	r.Body.Close()
	if r.StatusCode != 200 || !strings.Contains(string(b), `"name":"z2r_backup_20200101_000000.tar"`) {
		t.Fatalf("upload: %d %s", r.StatusCode, b)
	}
	if _, err := os.Stat(filepath.Join(e.app.Paths.BackupDir, "z2r_backup_20200101_000000.tar")); err != nil {
		t.Error(err)
	}

	bad := []byte("not a tar archive")
	req, _ = http.NewRequest(http.MethodPost, e.ts.URL+"/cgi-bin/backups.cgi?action=upload&name=x.tar", bytes.NewReader(bad))
	r, _ = e.ts.Client().Do(req)
	b, _ = io.ReadAll(r.Body)
	r.Body.Close()
	if r.StatusCode != 400 || !strings.Contains(string(b), "Файл не является архивом бэкапа") {
		t.Errorf("bad upload: %d %s", r.StatusCode, b)
	}
	other := buildTar(t, map[string]string{"random.txt": "x"})
	req, _ = http.NewRequest(http.MethodPost, e.ts.URL+"/cgi-bin/backups.cgi?action=upload&name=x.tar", bytes.NewReader(other))
	r, _ = e.ts.Client().Do(req)
	r.Body.Close()
	if r.StatusCode != 400 {
		t.Errorf("tar без config/lists: %d", r.StatusCode)
	}

	// Restore (webui layout).
	e.post("/cgi-bin/domains.cgi", "list", "netrogat", "action", "clear")
	resp, out = e.post("/cgi-bin/backups.cgi", "action", "restore", "name", name, "mode", "full")
	wantOK(t, resp, out)
	if out["files"] == nil || out["files"].(float64) < 1 {
		t.Errorf("restore files = %v", out["files"])
	}
	_, listOut := e.get("/cgi-bin/domains.cgi?list=netrogat")
	itemsList, _ := listOut["items"].([]any)
	found := false
	for _, it := range itemsList {
		if it.(map[string]any)["value"] == "avito.ru" {
			found = true
		}
	}
	if !found {
		t.Errorf("после restore netrogat без avito.ru: %v", listOut["items"])
	}
	resp, out = e.post("/cgi-bin/backups.cgi", "action", "restore", "name", "missing.tar")
	wantError(t, resp, out, 404, "Бэкап не найден")

	// Delete.
	resp, out = e.post("/cgi-bin/backups.cgi", "action", "delete", "name", name)
	wantOK(t, resp, out)
	resp, out = e.post("/cgi-bin/backups.cgi", "action", "delete", "name", name)
	wantError(t, resp, out, 404, "Бэкап не найден")
	resp, out = e.post("/cgi-bin/backups.cgi", "action", "delete")
	wantError(t, resp, out, 400, "Не указано имя файла")
	resp, out = e.post("/cgi-bin/backups.cgi", "action", "explode")
	wantError(t, resp, out, 400, "Неизвестное действие")
}

func wantError2(t *testing.T, resp *http.Response, body []byte, status int, msg string) {
	t.Helper()
	if resp.StatusCode != status || !strings.Contains(string(body), msg) {
		t.Fatalf("status=%d body=%s, want %d %q", resp.StatusCode, body, status, msg)
	}
}
