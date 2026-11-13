package app

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

const (
	minPasswordLen = 8
	bcryptCost     = bcrypt.DefaultCost
	sessionBytes   = 32
)

var (
	// ErrPasswordTooShort — пароль короче минимума.
	ErrPasswordTooShort = errors.New("app: пароль слишком короткий (минимум 8 символов)")
	// ErrNoPassword — нет заданного пароля.
	ErrNoPassword = errors.New("app: пароль не задан")
)

// AuthRequired сообщает, нужна ли авторизация Web UI.
func (c Config) AuthRequired() bool {
	return c.AuthEnabled
}

// HasPassword — в конфиге есть bcrypt-хеш пароля.
func (c Config) HasPassword() bool {
	return strings.TrimSpace(c.PasswordHash) != ""
}

// SetPassword записывает bcrypt-хеш, ротирует session_token и включает auth.
func (a *App) SetPassword(password string) error {
	password = strings.TrimSpace(password)
	if len(password) < minPasswordLen {
		return ErrPasswordTooShort
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
	if err != nil {
		return fmt.Errorf("app: bcrypt: %w", err)
	}
	session, err := randomHex(sessionBytes)
	if err != nil {
		return err
	}
	a.Config.PasswordHash = string(hash)
	a.Config.SessionToken = session
	a.Config.AuthEnabled = true
	return nil
}

// ClearAuth отключает проверку доступа (пароль/session можно оставить для повторного enable).
func (a *App) ClearAuth() {
	a.Config.AuthEnabled = false
}

// DisableAuthExplicitly — сознательный отказ от защиты (install -no-auth / auth disable).
func (a *App) DisableAuthExplicitly() {
	a.Config.AuthEnabled = false
	a.Config.PasswordHash = ""
	a.Config.SessionToken = ""
}

// EnableAuth включает проверку. Если пароля нет — ошибка ErrNoPassword.
func (a *App) EnableAuth() error {
	if !a.Config.HasPassword() {
		return ErrNoPassword
	}
	if a.Config.SessionToken == "" {
		session, err := randomHex(sessionBytes)
		if err != nil {
			return err
		}
		a.Config.SessionToken = session
	}
	a.Config.AuthEnabled = true
	return nil
}

// VerifyPassword проверяет plaintext-пароль против bcrypt-хеша.
func (a *App) VerifyPassword(password string) bool {
	if !a.Config.HasPassword() {
		return false
	}
	err := bcrypt.CompareHashAndPassword([]byte(a.Config.PasswordHash), []byte(password))
	return err == nil
}

// RotateSession выдаёт новый session_token (logout / смена пароля уже ротирует).
func (a *App) RotateSession() error {
	session, err := randomHex(sessionBytes)
	if err != nil {
		return err
	}
	a.Config.SessionToken = session
	return nil
}

// SessionEqual — constant-time сравнение с session_token.
func (a *App) SessionEqual(got string) bool {
	want := a.Config.SessionToken
	if want == "" || got == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(want), []byte(got)) == 1
}

func randomHex(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("app: rand: %w", err)
	}
	return hex.EncodeToString(buf), nil
}
