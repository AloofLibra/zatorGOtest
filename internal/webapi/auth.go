package webapi

import (
	"net/http"
	"path"
	"strings"
)

func isPublicAuthPath(p string) bool {
	switch p {
	case "/api/login", "/cgi-bin/login.cgi":
		return true
	default:
		return false
	}
}

// isStaticAsset — index/js/css/картинки SPA без auth (логин только во Vue).
func isStaticAsset(p string) bool {
	if p == "/" || p == "/index.html" {
		return true
	}
	base := path.Base(p)
	switch {
	case strings.HasSuffix(base, ".js"),
		strings.HasSuffix(base, ".css"),
		strings.HasSuffix(base, ".map"),
		strings.HasSuffix(base, ".svg"),
		strings.HasSuffix(base, ".png"),
		strings.HasSuffix(base, ".ico"),
		strings.HasSuffix(base, ".woff"),
		strings.HasSuffix(base, ".woff2"):
		return true
	default:
		return false
	}
}

func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request, p params) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "Метод не поддерживается")
		return
	}
	if !s.app.Config.AuthRequired() {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "auth_required": false})
		return
	}
	pass := p.Get("password")
	if !s.app.VerifyPassword(pass) {
		writeError(w, http.StatusUnauthorized, "Неверный пароль")
		return
	}
	if s.app.Config.SessionToken == "" {
		if err := s.app.RotateSession(); err != nil {
			writeErr(w, err)
			return
		}
		if err := s.app.Save(); err != nil {
			writeErr(w, err)
			return
		}
	}
	s.setSessionCookie(w)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request, _ params) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "Метод не поддерживается")
		return
	}
	_ = s.app.RotateSession()
	_ = s.app.Save()
	s.clearSessionCookie(w)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
