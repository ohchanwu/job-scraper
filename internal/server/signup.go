package server

import (
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"net/http"

	"github.com/ohchanwu/jobcron/internal/auth"
	"github.com/ohchanwu/jobcron/internal/storage"
)

const signupErrorCopy = "가입 정보를 확인해주세요."

type signupPage struct {
	Error     string
	Closed    bool
	CSRFToken string
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
	email := auth.NormalizeEmail(r.FormValue("email"))
	ip := s.clientIP(r)
	if !s.signupLimiter.reserveFailure(ip, email) {
		http.Error(w, "too many signup attempts", http.StatusTooManyRequests)
		return
	}
	password := r.FormValue("password")
	if !signupCodeMatches(s.signupAccessCode, r.FormValue("access_code")) ||
		auth.ValidateEmail(email) != nil ||
		auth.ValidatePassword(password) != nil ||
		password != r.FormValue("password_confirm") {
		s.renderSignupFailure(w, r)
		return
	}
	if _, exists, err := s.store.UserByEmail(r.Context(), email); err != nil {
		http.Error(w, "signup failed", http.StatusInternalServerError)
		return
	} else if exists {
		s.renderSignupFailure(w, r)
		return
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		http.Error(w, "signup failed", http.StatusInternalServerError)
		return
	}
	user, err := s.store.CreateUser(r.Context(), email, hash)
	if errors.Is(err, storage.ErrEmailAlreadyExists) {
		s.renderSignupFailure(w, r)
		return
	}
	if err != nil {
		http.Error(w, "signup failed", http.StatusInternalServerError)
		return
	}
	if err := s.startSession(w, r.Context(), user.ID); err != nil {
		http.Error(w, "signup failed", http.StatusInternalServerError)
		return
	}
	s.signupLimiter.reset(ip, email)
	http.Redirect(w, r, "/profile", http.StatusSeeOther)
}

func (s *Server) renderSignupFailure(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusUnprocessableEntity)
	s.renderWithRequest(w, r, "signup.html", signupPage{Error: signupErrorCopy})
}

func signupCodeMatches(configured, submitted string) bool {
	want := sha256.Sum256([]byte(configured))
	got := sha256.Sum256([]byte(submitted))
	return subtle.ConstantTimeCompare(want[:], got[:]) == 1
}
