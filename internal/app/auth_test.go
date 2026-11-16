package app

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSetPasswordVerifyAndUpdatePreserves(t *testing.T) {
	a := newTestApp(t)
	if err := a.SetPassword("secret12"); err != nil {
		t.Fatal(err)
	}
	if !a.Config.AuthEnabled || !a.Config.HasPassword() || a.Config.SessionToken == "" {
		t.Fatalf("after set: %+v", a.Config)
	}
	if !a.VerifyPassword("secret12") || a.VerifyPassword("wrong") {
		t.Fatal("verify")
	}
	session := a.Config.SessionToken
	hash := a.Config.PasswordHash
	if err := a.Install(); err != nil {
		t.Fatal(err)
	}
	if err := a.Update(); err != nil {
		t.Fatal(err)
	}
	if a.Config.PasswordHash != hash || a.Config.SessionToken != session || !a.Config.AuthEnabled {
		t.Fatalf("update cleared auth: %+v", a.Config)
	}
}

func TestDisableEnableAuth(t *testing.T) {
	a := newTestApp(t)
	if err := a.SetPassword("secret12"); err != nil {
		t.Fatal(err)
	}
	a.ClearAuth()
	if a.Config.AuthRequired() || !a.Config.HasPassword() {
		t.Fatalf("clear should disable but keep hash: %+v", a.Config)
	}
	if err := a.EnableAuth(); err != nil {
		t.Fatal(err)
	}
	if !a.Config.AuthRequired() {
		t.Fatal("enable failed")
	}
	a.DisableAuthExplicitly()
	if a.Config.HasPassword() || a.Config.AuthRequired() {
		t.Fatalf("explicit disable should wipe: %+v", a.Config)
	}
	if err := a.EnableAuth(); err != ErrNoPassword {
		t.Fatalf("enable without password: %v", err)
	}
}

func TestAuthEnabledFromPasswordHash(t *testing.T) {
	a := newTestApp(t)
	if err := os.MkdirAll(filepath.Dir(a.ConfigPath), 0o755); err != nil {
		t.Fatal(err)
	}
	raw := `{"port":17682,"password_hash":"$2a$10$abcdefghijklmnopqrstuu","zapret_source":"fork"}` + "\n"
	if err := os.WriteFile(a.ConfigPath, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := a.Load(); err != nil {
		t.Fatal(err)
	}
	if !a.Config.AuthEnabled || !a.Config.HasPassword() {
		t.Fatalf("migration from password_hash: %+v", a.Config)
	}
}

func TestMigrateOrchestraCacheToState(t *testing.T) {
	a := newTestApp(t)
	if err := a.Install(); err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(a.Paths.ExtraStratsDir, "cache", "orchestra")
	oldScopes := filepath.Join(old, "scopes", "clients")
	if err := os.MkdirAll(oldScopes, 0o755); err != nil {
		t.Fatal(err)
	}
	legacyFiles := map[string]string{
		"locked.tsv":        "9\ttls\t4\n",
		"blob_override.tsv": "2\tclone\n",
		"mode_override.tsv": "2\tclone\n",
		"sni_override.tsv":  "2\twww.google.com\n",
		"clonesize.tsv":     "2\t1200\n",
		"auto_locked.tsv":   "9\ttls\t4\n",
		"locked.manual.tsv": "7\ttls\t2\n",
		"locked.lua":        "must not migrate\n",
		"ignored_directory": "",
	}
	for name, content := range legacyFiles {
		if name == "ignored_directory" {
			if err := os.MkdirAll(filepath.Join(old, name), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(old, name, "value.tsv"), []byte("ignore\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := os.WriteFile(filepath.Join(old, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(oldScopes, "profile2.tsv"), []byte("scope legacy\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Пустые файлы в новом state не должны блокировать миграцию.
	stateDir := a.Paths.StateOrchestraDir()
	for _, name := range []string{"locked.tsv", "blob_override.tsv", "sni_override.tsv", "clonesize.tsv", "auto_locked.tsv", "locked.manual.tsv"} {
		if err := os.WriteFile(filepath.Join(stateDir, name), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Непустой файл нового state имеет приоритет над legacy.
	if err := os.WriteFile(filepath.Join(stateDir, "mode_override.tsv"), []byte("3\tfake\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(stateDir, "scopes", "clients"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "scopes", "clients", "profile2.tsv"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := a.Update(); err != nil {
		t.Fatal(err)
	}
	for name, want := range legacyFiles {
		if name == "locked.lua" || name == "ignored_directory" || name == "mode_override.tsv" {
			continue
		}
		got, err := os.ReadFile(filepath.Join(stateDir, name))
		if err != nil || string(got) != want {
			t.Errorf("%s migrated = %q err=%v, want %q", name, got, err, want)
		}
	}
	got, err := os.ReadFile(filepath.Join(stateDir, "mode_override.tsv"))
	if err != nil || string(got) != "3\tfake\n" {
		t.Errorf("existing override overwritten: %q err=%v", got, err)
	}
	got, err = os.ReadFile(filepath.Join(stateDir, "scopes", "clients", "profile2.tsv"))
	if err != nil || string(got) != "scope legacy\n" {
		t.Errorf("scope not migrated into empty state: %q err=%v", got, err)
	}
	if _, err := os.Stat(filepath.Join(stateDir, "locked.lua")); !os.IsNotExist(err) {
		t.Errorf("legacy Lua unexpectedly migrated: %v", err)
	}
	if _, err := os.Stat(filepath.Join(stateDir, "ignored_directory")); !os.IsNotExist(err) {
		t.Errorf("unrecognized directory unexpectedly migrated: %v", err)
	}
}

func TestUninstallKeepVsPurge(t *testing.T) {
	a := newTestApp(t)
	if err := a.SetPassword("secret12"); err != nil {
		t.Fatal(err)
	}
	if err := a.Install(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(a.Paths.ListsDir, "netrogat.txt"), []byte("keep.example\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := a.Uninstall(UninstallOptions{SkipService: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(a.Paths.ZatorConfig); err != nil {
		t.Fatalf("config должен остаться: %v", err)
	}
	if _, err := os.Stat(filepath.Join(a.Paths.ListsDir, "netrogat.txt")); err != nil {
		t.Fatalf("lists должны остаться: %v", err)
	}
	if _, err := os.Stat(a.Paths.WebUIDir); !os.IsNotExist(err) {
		t.Errorf("webui должен быть удалён: %v", err)
	}
	if err := a.Uninstall(UninstallOptions{Purge: true, SkipService: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(a.Paths.ZatorRoot); !os.IsNotExist(err) {
		t.Errorf("purge должен снести корень: %v", err)
	}
}
