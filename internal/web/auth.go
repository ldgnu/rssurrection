package web

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	sessionCookie = "rssr_session"
	sessionTTL    = 7 * 24 * time.Hour
	loginPath     = "/login"
)

type loginLimiter struct {
	mu    sync.Mutex
	fails map[string][]time.Time
}

func (l *loginLimiter) blocked(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	recent := l.fails[key][:0]
	for _, t := range l.fails[key] {
		if now.Sub(t) < 5*time.Minute {
			recent = append(recent, t)
		}
	}
	l.fails[key] = recent
	return len(recent) >= 5
}

func (l *loginLimiter) fail(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.fails[key] = append(l.fails[key], time.Now())
}

func (l *loginLimiter) reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.fails, key)
}

func (s *Server) authEnabled() bool {
	return s.cfg.AuthUser != "" && s.cfg.AuthPassword != ""
}

func (s *Server) sessionToken(exp int64) string {
	mac := hmac.New(sha256.New, []byte(s.cfg.SigningSecret))
	fmt.Fprintf(mac, "%d", exp)
	return strconv.FormatInt(exp, 10) + "." + hex.EncodeToString(mac.Sum(nil))
}

func (s *Server) validSession(r *http.Request) bool {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return false
	}
	parts := strings.SplitN(c.Value, ".", 2)
	if len(parts) != 2 {
		return false
	}
	exp, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || time.Now().Unix() > exp {
		return false
	}
	want := s.sessionToken(exp)
	return subtle.ConstantTimeCompare([]byte(c.Value), []byte(want)) == 1
}

func (s *Server) secureRequest(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	return strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

// sessionAuth redirects anonymous visitors to the login panel.
func (s *Server) sessionAuth(next http.Handler) http.Handler {
	if !s.authEnabled() {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		switch {
		case p == loginPath, p == "/healthz", p == "/readyz", p == "/sw.js",
			strings.HasPrefix(p, "/static/"), p == "/favicon.ico":
			next.ServeHTTP(w, r)
			return
		}
		if s.validSession(r) {
			next.ServeHTTP(w, r)
			return
		}
		http.Redirect(w, r, loginPath+"?next="+r.URL.Path, http.StatusSeeOther)
	})
}

func (s *Server) handleLoginPage(w http.ResponseWriter, r *http.Request) {
	if !s.authEnabled() || s.validSession(r) {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	s.renderLogin(w, r, http.StatusOK, "")
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if !s.authEnabled() {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	ip, _, _ := net.SplitHostPort(r.RemoteAddr)
	if s.limiter.blocked(ip) {
		s.renderLogin(w, r, http.StatusTooManyRequests, "Demasiados intentos. Esperá unos minutos.")
		return
	}
	if err := r.ParseForm(); err != nil {
		s.renderLogin(w, r, http.StatusBadRequest, "Formulario inválido.")
		return
	}
	user := strings.TrimSpace(r.PostFormValue("usuario"))
	pass := r.PostFormValue("clave")

	okUser := subtle.ConstantTimeCompare([]byte(user), []byte(s.cfg.AuthUser)) == 1
	okPass := subtle.ConstantTimeCompare([]byte(pass), []byte(s.cfg.AuthPassword)) == 1
	if !okUser || !okPass {
		s.limiter.fail(ip)
		s.log.Warn("login failed", "user", user)
		time.Sleep(300 * time.Millisecond)
		s.renderLogin(w, r, http.StatusUnauthorized, "Usuario o contraseña incorrectos.")
		return
	}

	s.limiter.reset(ip)
	exp := time.Now().Add(sessionTTL).Unix()
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    s.sessionToken(exp),
		Path:     "/",
		HttpOnly: true,
		Secure:   s.secureRequest(r),
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Unix(exp, 0),
	})
	next := r.PostFormValue("next")
	if next == "" || !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") {
		next = "/"
	}
	http.Redirect(w, r, next, http.StatusSeeOther)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   s.secureRequest(r),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
	http.Redirect(w, r, loginPath, http.StatusSeeOther)
}

type loginData struct {
	Error string
	Next  string
}

func (s *Server) renderLogin(w http.ResponseWriter, r *http.Request, status int, msg string) {
	t, ok := s.pages["login"]
	if !ok {
		http.Error(w, "login no disponible", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = t.ExecuteTemplate(w, "layout", loginData{Error: msg, Next: r.URL.Query().Get("next")})
}
