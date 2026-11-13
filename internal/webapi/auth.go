package webapi

import (
	"net/http"
	"path"
	"strings"
	"sync"
	"time"
)

func isPublicAuthPath(p string) bool {
	switch p {
	case "/api/login", "/cgi-bin/login.cgi":
		// logout намеренно НЕ public: без сессии только чистим cookie, без RotateSession.
		return true
	default:
		return false
	}
}

// hasAssetExt — известные статические расширения SPA.
func hasAssetExt(base string) bool {
	switch {
	case strings.HasSuffix(base, ".js"),
		strings.HasSuffix(base, ".css"),
		strings.HasSuffix(base, ".map"),
		strings.HasSuffix(base, ".svg"),
		strings.HasSuffix(base, ".png"),
		strings.HasSuffix(base, ".ico"),
		strings.HasSuffix(base, ".woff"),
		strings.HasSuffix(base, ".woff2"),
		strings.HasSuffix(base, ".html"):
		return true
	default:
		return false
	}
}

// isAPIPath — защищённые JSON-эндпоинты (не SPA shell).
func isAPIPath(p string) bool {
	return strings.HasPrefix(p, "/api/") || strings.HasPrefix(p, "/cgi-bin/")
}

// isStaticAsset — index/js/css/картинки и SPA deep-link без auth (логин только во Vue).
func isStaticAsset(p string) bool {
	if p == "/" || p == "/index.html" {
		return true
	}
	if isAPIPath(p) {
		return false
	}
	base := path.Base(p)
	if hasAssetExt(base) {
		return true
	}
	// Путь без «файлового» расширения → SPA fallback на index.html.
	if !strings.Contains(base, ".") {
		return true
	}
	return false
}

func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Logout: всегда пропускаем до handler — там сами решим rotate vs clear-cookie-only.
		if r.URL.Path == "/api/logout" || r.URL.Path == "/cgi-bin/logout.cgi" {
			next.ServeHTTP(w, r)
			return
		}
		if isPublicAuthPath(r.URL.Path) || isStaticAsset(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		if !s.app.Config.AuthRequired() {
			next.ServeHTTP(w, r)
			return
		}
		ok, setCookie := s.authorized(r)
		if !ok {
			w.Header().Set("WWW-Authenticate", `Basic realm="ZatorGO"`)
			writeError(w, http.StatusUnauthorized, "Требуется авторизация")
			return
		}
		if setCookie {
			s.setSessionCookie(w)
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) setSessionCookie(w http.ResponseWriter) {
	tok := s.app.Config.SessionToken
	if tok == "" {
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    tok,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   30 * 24 * 3600,
	})
}

func (s *Server) clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		MaxAge:   -1,
	})
}

// authorized: session cookie / Bearer session / Basic с паролем.
func (s *Server) authorized(r *http.Request) (ok, setCookie bool) {
	if h := r.Header.Get("Authorization"); h != "" {
		if scheme, rest, found := strings.Cut(h, " "); found {
			switch strings.ToLower(scheme) {
			case "bearer":
				if s.app.SessionEqual(strings.TrimSpace(rest)) {
					return true, false
				}
			case "basic":
				if _, pass, ok := r.BasicAuth(); ok && s.app.VerifyPassword(pass) {
					return true, true
				}
			}
		}
	}
	if c, err := r.Cookie(CookieName); err == nil && s.app.SessionEqual(c.Value) {
		return true, false
	}
	return false, false
}

// --- login rate limit (in-memory, per-IP) ---

const (
	loginMaxFails   = 8
	loginLockWindow = 5 * time.Minute
)

type loginAttempt struct {
	fails    int
	lockedUntil time.Time
}

var (
	loginMu       sync.Mutex
	loginAttempts = map[string]*loginAttempt{}
)

func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if i := strings.IndexByte(xff, ','); i >= 0 {
			return strings.TrimSpace(xff[:i])
		}
		return strings.TrimSpace(xff)
	}
	host := r.RemoteAddr
	if i := strings.LastIndex(host, ":"); i >= 0 {
		return host[:i]
	}
	return host
}

func loginAllowed(ip string) bool {
	loginMu.Lock()
	defer loginMu.Unlock()
	a := loginAttempts[ip]
	if a == nil {
		return true
	}
	if time.Now().Before(a.lockedUntil) {
		return false
	}
	if !a.lockedUntil.IsZero() && time.Now().After(a.lockedUntil) {
		delete(loginAttempts, ip)
	}
	return true
}

func loginFail(ip string) {
	loginMu.Lock()
	defer loginMu.Unlock()
	a := loginAttempts[ip]
	if a == nil {
		a = &loginAttempt{}
		loginAttempts[ip] = a
	}
	a.fails++
	if a.fails >= loginMaxFails {
		a.lockedUntil = time.Now().Add(loginLockWindow)
		a.fails = 0
	}
}

func loginOK(ip string) {
	loginMu.Lock()
	defer loginMu.Unlock()
	delete(loginAttempts, ip)
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request, p params) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "Метод не поддерживается")
		return
	}
	if !s.app.Config.AuthRequired() {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "auth_required": false})
		return
	}
	ip := clientIP(r)
	if !loginAllowed(ip) {
		writeError(w, http.StatusTooManyRequests, "Слишком много попыток входа. Подождите несколько минут.")
		return
	}
	pass := p.Get("password")
	if !s.app.VerifyPassword(pass) {
		loginFail(ip)
		writeError(w, http.StatusUnauthorized, "Неверный пароль")
		return
	}
	loginOK(ip)
	// Всегда ротируем session при успешном входе (украденный токен перестаёт работать).
	if err := s.app.RotateSession(); err != nil {
		writeErr(w, err)
		return
	}
	if err := s.app.Save(); err != nil {
		writeErr(w, err)
		return
	}
	s.setSessionCookie(w)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "auth_required": true})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request, _ params) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "Метод не поддерживается")
		return
	}
	// Без auth — только сброс cookie в браузере.
	if !s.app.Config.AuthRequired() {
		s.clearSessionCookie(w)
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "auth_required": false})
		return
	}
	ok, _ := s.authorized(r)
	if ok {
		_ = s.app.RotateSession()
		_ = s.app.Save()
	}
	// Без валидной сессии: только clear cookie, session_token НЕ трогаем (anti-CSRF logout).
	s.clearSessionCookie(w)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
