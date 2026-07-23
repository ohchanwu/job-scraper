package server

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"net/http"

	"github.com/ohchanwu/jobcron/internal/auth"
	"github.com/ohchanwu/jobcron/internal/storage"
)

const (
	signupErrorCopy                = "가입 정보를 확인해주세요."
	signupMaxFormBytes       int64 = 16 << 10
	signupMaxEmailBytes            = 254
	signupMaxAccessCodeBytes       = 256
	passwordWorkConcurrency        = 2
)

type signupPage struct {
	Error     string
	Closed    bool
	CSRFToken string
}

func limitSignupBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/signup" {
			r.Body = http.MaxBytesReader(w, r.Body, signupMaxFormBytes)
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) handleSignupForm(w http.ResponseWriter, r *http.Request) {
	s.renderWithRequest(w, r, "signup.html", signupPage{Closed: s.signupAccessCode == ""})
}

func (s *Server) handleSignupPost(w http.ResponseWriter, r *http.Request) {
	if s.signupAccessCode == "" {
		w.WriteHeader(http.StatusForbidden)
		s.renderWithRequest(w, r, "signup.html", signupPage{Closed: true})
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid signup form", http.StatusBadRequest)
		return
	}
	rawEmail := r.FormValue("email")
	email := auth.NormalizeEmail(rawEmail)
	password := r.FormValue("password")
	passwordConfirm := r.FormValue("password_confirm")
	accessCode := r.FormValue("access_code")
	if len(rawEmail) > signupMaxEmailBytes ||
		len(password) > auth.MaxPasswordBytes ||
		len(passwordConfirm) > auth.MaxPasswordBytes ||
		len(accessCode) > signupMaxAccessCodeBytes ||
		auth.ValidateEmail(email) != nil ||
		auth.ValidatePassword(password) != nil ||
		password != passwordConfirm {
		s.renderSignupFailure(w, r)
		return
	}
	ip := s.clientIP(r)
	if !s.signupLimiter.reserveIP(ip) {
		http.Error(w, "too many signup attempts", http.StatusTooManyRequests)
		return
	}
	if !signupCodeMatches(s.signupAccessCode, accessCode) {
		s.renderSignupFailure(w, r)
		return
	}
	if !s.acquirePasswordWork(r.Context()) {
		http.Error(w, "too many signup attempts", http.StatusTooManyRequests)
		return
	}
	hash, err := auth.HashPassword(password)
	s.releasePasswordWork()
	if err != nil {
		http.Error(w, "signup failed", http.StatusInternalServerError)
		return
	}
	token, tokenHash, expiresAt, err := newSessionToken()
	if err != nil {
		http.Error(w, "signup failed", http.StatusInternalServerError)
		return
	}
	_, err = s.store.CreateUserWithSession(r.Context(), email, hash, tokenHash, expiresAt)
	if errors.Is(err, storage.ErrEmailAlreadyExists) {
		s.renderSignupFailure(w, r)
		return
	}
	if err != nil {
		http.Error(w, "signup failed", http.StatusInternalServerError)
		return
	}
	http.SetCookie(w, s.sessionCookie(token, expiresAt))
	s.signupLimiter.resetIP(ip)
	http.Redirect(w, r, "/profile", http.StatusSeeOther)
}

func (s *Server) acquirePasswordWork(ctx context.Context) bool {
	if ctx.Err() != nil {
		return false
	}
	select {
	case s.passwordWorkSlots <- struct{}{}:
		return true
	default:
		return false
	}
}

func (s *Server) releasePasswordWork() { <-s.passwordWorkSlots }

func (s *Server) renderSignupFailure(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusUnprocessableEntity)
	s.renderWithRequest(w, r, "signup.html", signupPage{Error: signupErrorCopy})
}

func signupCodeMatches(configured, submitted string) bool {
	want := sha256.Sum256([]byte(configured))
	got := sha256.Sum256([]byte(submitted))
	return subtle.ConstantTimeCompare(want[:], got[:]) == 1
}
