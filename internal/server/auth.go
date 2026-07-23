package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/ohchanwu/jobcron/internal/auth"
	"github.com/ohchanwu/jobcron/internal/storage"
)

const (
	sessionCookieName = "jobcron_session"
	sessionTTL        = 30 * 24 * time.Hour
	loginErrorCopy    = "이메일 또는 비밀번호를 확인해주세요."
)

var errSessionNotCreated = errors.New("server: authenticated user changed before session creation")

type loginPage struct {
	Error     string
	Email     string
	CSRFToken string
}

func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if publicAuthPath(r) {
			next.ServeHTTP(w, r)
			return
		}
		if _, ok := s.userFromRequest(r.Context(), r); ok {
			next.ServeHTTP(w, r)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/") {
			writeAuthUnauthorized(w)
			return
		}
		http.Redirect(w, r, "/login", http.StatusSeeOther)
	})
}

func publicAuthPath(r *http.Request) bool {
	if (r.URL.Path == "/login" || r.URL.Path == "/signup") &&
		(r.Method == http.MethodGet || r.Method == http.MethodPost) {
		return true
	}
	if r.URL.Path == "/logout" && r.Method == http.MethodPost {
		return true
	}
	if r.URL.Path == "/favicon.ico" {
		return true
	}
	return strings.HasPrefix(r.URL.Path, "/static/")
}

func (s *Server) userFromRequest(ctx context.Context, r *http.Request) (userID int64, ok bool) {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil || cookie.Value == "" {
		return 0, false
	}
	user, ok, err := s.store.UserBySessionToken(ctx, cookie.Value)
	if err != nil || !ok {
		return 0, false
	}
	return user.ID, true
}

func (s *Server) stateUserID(ctx context.Context, r *http.Request) (int64, error) {
	if !s.productionMode {
		if s.store.Dialect() == storage.DialectSQLite {
			return 0, nil
		}
		if s.localUserID <= 0 {
			return 0, fmt.Errorf("server: non-production PostgreSQL requires a positive local user ID")
		}
		return s.localUserID, nil
	}
	if s.demoMode {
		if s.store.Dialect() == storage.DialectSQLite {
			return 0, nil
		}
		if s.localUserID <= 0 {
			return 0, fmt.Errorf("server: production PostgreSQL demo requires a positive resolved user ID")
		}
		return s.localUserID, nil
	}
	userID, ok := s.userFromRequest(ctx, r)
	if !ok {
		return 0, http.ErrNoCookie
	}
	return userID, nil
}

func (s *Server) handleLoginForm(w http.ResponseWriter, r *http.Request) {
	s.renderWithRequest(w, r, "login.html", loginPage{})
}

func (s *Server) handleLoginPost(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	email := strings.TrimSpace(r.FormValue("email"))
	password := r.FormValue("password")
	ip := s.clientIP(r)
	if !s.loginIPLimiter.reserveIP(ip) || !s.loginLimiter.reserveFailure(ip, email) {
		http.Error(w, "too many login attempts", http.StatusTooManyRequests)
		return
	}
	user, ok, err := s.store.UserByEmail(r.Context(), email)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !ok {
		s.renderLoginFailure(w, r)
		return
	}
	if !s.acquirePasswordWork(r.Context()) {
		http.Error(w, "too many login attempts", http.StatusTooManyRequests)
		return
	}
	matches, err := auth.VerifyPassword(user.PasswordHash, password)
	s.releasePasswordWork()
	if err != nil || !matches {
		s.renderLoginFailure(w, r)
		return
	}
	if err := s.startSession(w, r.Context(), user.ID, user.PasswordHash); err != nil {
		if errors.Is(err, errSessionNotCreated) {
			s.renderLoginFailure(w, r)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.loginLimiter.reset(ip, email)
	s.loginIPLimiter.resetIP(ip)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// startSession creates one opaque browser session. Login supplies the exact
// password hash it verified so concurrent password changes retain their atomic
// guard; signup has just created the user and needs no prior-hash condition.
func (s *Server) startSession(w http.ResponseWriter, ctx context.Context, userID int64, verifiedHash ...string) error {
	token, tokenHash, expiresAt, err := newSessionToken()
	if err != nil {
		return err
	}
	if len(verifiedHash) > 0 {
		created, err := s.store.CreateSessionIfPasswordHash(ctx, userID, verifiedHash[0], tokenHash, expiresAt)
		if err != nil {
			return err
		}
		if !created {
			return errSessionNotCreated
		}
	} else if err := s.store.CreateSession(ctx, userID, tokenHash, expiresAt); err != nil {
		return err
	}
	http.SetCookie(w, s.sessionCookie(token, expiresAt))
	return nil
}

func newSessionToken() (token, tokenHash string, expiresAt time.Time, err error) {
	token, err = auth.GenerateSessionToken()
	if err != nil {
		return "", "", time.Time{}, err
	}
	return token, auth.HashSessionToken(token), time.Now().Add(sessionTTL), nil
}

func (s *Server) renderLoginFailure(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusUnauthorized)
	s.renderWithRequest(w, r, "login.html", loginPage{Error: loginErrorCopy})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(sessionCookieName); err == nil && cookie.Value != "" {
		if err := s.store.RevokeSessionToken(r.Context(), cookie.Value); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	cookie := s.sessionCookie("", time.Unix(0, 0))
	cookie.MaxAge = -1
	http.SetCookie(w, cookie)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func writeAuthUnauthorized(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusUnauthorized)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": "authentication required"})
}

func (s *Server) sessionCookie(token string, expiresAt time.Time) *http.Cookie {
	return &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		Expires:  expiresAt.UTC(),
		HttpOnly: true,
		Secure:   s.productionMode,
		SameSite: http.SameSiteLaxMode,
	}
}
