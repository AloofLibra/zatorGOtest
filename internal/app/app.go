// Package app содержит общий контекст приложения zator и его конфигурацию.
package app

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/AloofLibra/zator/internal/paths"
	"github.com/AloofLibra/zator/internal/platform"
)

const (
	// DefaultPort — порт Web UI по умолчанию.
	DefaultPort = 17682

	// ZapretSourceOfficial — официальный репозиторий zapret2.
	ZapretSourceOfficial = "official"
	// ZapretSourceFork — форк zapret2.
	ZapretSourceFork = "fork"
)

// Config — содержимое zator.json.
type Config struct {
	Port         int    `json:"port"`
	Token        string `json:"token"`
	Mirror       string `json:"mirror"`
	ZapretSource string `json:"zapret_source"`
}

// DefaultConfig возвращает конфигурацию по умолчанию.
func DefaultConfig() Config {
	return Config{
		Port:         DefaultPort,
		ZapretSource: ZapretSourceFork,
	}
}

// Validate проверяет корректность значений и подставляет значения по умолчанию.
func (c *Config) Validate() error {
	if c.Port == 0 {
		c.Port = DefaultPort
	}
	if c.Port < 1 || c.Port > 65535 {
		return fmt.Errorf("app: некорректный порт %d", c.Port)
	}
	switch c.ZapretSource {
	case "":
		c.ZapretSource = ZapretSourceFork
	case ZapretSourceOfficial, ZapretSourceFork:
	default:
		return fmt.Errorf("app: неизвестный источник zapret %q (official|fork)", c.ZapretSource)
	}
	return nil
}

// App — общий контекст: пути, платформа и конфигурация.
type App struct {
	Paths      paths.Paths
	Platform   platform.Platform
	ConfigPath string // путь к zator.json
	Config     Config
}

// New создаёт App с раскладкой из переменных окружения, определённой
// платформой и конфигурацией по умолчанию (без чтения с диска).
func New() *App {
	return NewWith(paths.FromEnv(), platform.Detect())
}

// NewWith создаёт App из готовых путей и платформы.
func NewWith(p paths.Paths, pl platform.Platform) *App {
	return &App{
		Paths:      p,
		Platform:   pl,
		ConfigPath: p.ZatorConfig,
		Config:     DefaultConfig(),
	}
}

// Load создаёт App и загружает zator.json, если файл существует.
func Load() (*App, error) {
	a := New()
	if err := a.Load(); err != nil {
		return a, err
	}
	return a, nil
}

// Load читает конфигурацию из ConfigPath. Если файла нет, остаётся
// конфигурация по умолчанию и ошибка не возвращается.
func (a *App) Load() error {
	data, err := os.ReadFile(a.ConfigPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			a.Config = DefaultConfig()
			return nil
		}
		return fmt.Errorf("app: чтение %s: %w", a.ConfigPath, err)
	}
	cfg := DefaultConfig()
	if err := json.Unmarshal(data, &cfg); err != nil {
		return fmt.Errorf("app: разбор %s: %w", a.ConfigPath, err)
	}
	if err := cfg.Validate(); err != nil {
		return err
	}
	a.Config = cfg
	return nil
}

// Save атомарно записывает конфигурацию в ConfigPath (права 0600 — файл содержит токен).
func (a *App) Save() error {
	if err := a.Config.Validate(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(a.Config, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	dir := filepath.Dir(a.ConfigPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".zator-*.json.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	_ = os.Chmod(tmpName, 0o600)
	return os.Rename(tmpName, a.ConfigPath)
}

// EnsureToken генерирует случайный токен, если он ещё не задан.
func (a *App) EnsureToken() error {
	if a.Config.Token != "" {
		return nil
	}
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return err
	}
	a.Config.Token = hex.EncodeToString(buf)
	return nil
}

// WebUIURL возвращает URL Web UI (с токеном, если он задан).
func (a *App) WebUIURL(host string) string {
	if host == "" {
		host = "localhost"
	}
	u := fmt.Sprintf("http://%s:%d/", host, a.Config.Port)
	if a.Config.Token != "" {
		u += "?token=" + a.Config.Token
	}
	return u
}

// Installed сообщает, установлен ли zator.
func (a *App) Installed() bool { return a.Paths.Installed() }
