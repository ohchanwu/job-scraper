package server

import (
	"net/http"
	"time"

	"github.com/ohchanwu/jobcron/internal/auth"
	"github.com/ohchanwu/jobcron/internal/storage"
)

const (
	accountErrorCopy          = "입력한 계정 정보를 확인해주세요."
	accountMaxFormBytes int64 = 4 << 10
)

type accountPage struct {
	Email     string
	Error     string
	CSRFToken string
}

func limitAccountBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost &&
			(r.URL.Path == "/account/password" || r.URL.Path == "/account/delete") {
			r.Body = http.MaxBytesReader(w, r.Body, accountMaxFormBytes)
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) handleAccount(w http.ResponseWriter, r *http.Request) {
	user, _, ok := s.accountUser(r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	s.renderAccount(w, r, http.StatusOK, accountPage{Email: user.Email})
}

func (s *Server) handleAccountPassword(w http.ResponseWriter, r *http.Request) {
	user, rawSession, ok := s.accountUser(r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid account form", http.StatusBadRequest)
		return
	}
	current := r.FormValue("current_password")
	replacement := r.FormValue("new_password")
	confirmation := r.FormValue("password_confirm")
	if len(current) > auth.MaxPasswordBytes ||
		auth.ValidatePassword(replacement) != nil ||
		len(confirmation) > auth.MaxPasswordBytes ||
		replacement != confirmation ||
		!passwordMatches(user.PasswordHash, current) {
		s.renderAccountFailure(w, r, user.Email)
		return
	}
	hash, err := auth.HashPassword(replacement)
	if err != nil {
		http.Error(w, "password change failed", http.StatusInternalServerError)
		return
	}
	if err := s.store.ChangePassword(r.Context(), user.ID, hash, auth.HashSessionToken(rawSession)); err != nil {
		http.Error(w, "password change failed", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/account", http.StatusSeeOther)
}

func (s *Server) handleAccountDelete(w http.ResponseWriter, r *http.Request) {
	user, _, ok := s.accountUser(r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid account form", http.StatusBadRequest)
		return
	}
	current := r.FormValue("current_password")
	confirmedEmail := auth.NormalizeEmail(r.FormValue("confirm_email"))
	if len(current) > auth.MaxPasswordBytes ||
		confirmedEmail != user.Email ||
		!passwordMatches(user.PasswordHash, current) {
		s.renderAccountFailure(w, r, user.Email)
		return
	}
	deleted, err := s.store.DeleteUser(r.Context(), user.ID)
	if err != nil || !deleted {
		http.Error(w, "account deletion failed", http.StatusInternalServerError)
		return
	}
	cookie := s.sessionCookie("", time.Unix(0, 0))
	cookie.MaxAge = -1
	http.SetCookie(w, cookie)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (s *Server) accountUser(r *http.Request) (storage.User, string, bool) {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil || cookie.Value == "" {
		return storage.User{}, "", false
	}
	user, ok, err := s.store.UserBySessionToken(r.Context(), cookie.Value)
	return user, cookie.Value, err == nil && ok
}

func passwordMatches(hash, password string) bool {
	matches, err := auth.VerifyPassword(hash, password)
	return err == nil && matches
}

func (s *Server) renderAccountFailure(w http.ResponseWriter, r *http.Request, email string) {
	s.renderAccount(w, r, http.StatusUnprocessableEntity, accountPage{Email: email, Error: accountErrorCopy})
}

func (s *Server) renderAccount(w http.ResponseWriter, r *http.Request, status int, page accountPage) {
	page.CSRFToken = s.csrfTokenForRequest(w, r)
	w.WriteHeader(status)
	s.render(w, "account.html", page)
}
