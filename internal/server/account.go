package server

import (
	"net/http"
	"strconv"
	"time"

	"github.com/ohchanwu/jobcron/internal/auth"
	"github.com/ohchanwu/jobcron/internal/storage"
)

const (
	accountErrorCopy          = "입력한 계정 정보를 확인해주세요."
	accountMaxFormBytes int64 = 16 << 10
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
		replacement != confirmation {
		s.renderAccountFailure(w, r, user.Email)
		return
	}
	ip, allowed := s.reserveAccountMutation(r, user.ID)
	if !allowed {
		http.Error(w, accountErrorCopy, http.StatusTooManyRequests)
		return
	}
	if !s.acquirePasswordWork(r.Context()) {
		http.Error(w, accountErrorCopy, http.StatusTooManyRequests)
		return
	}
	matches := passwordMatches(user.PasswordHash, current)
	if matches {
		s.resetAccountMutation(ip, user.ID)
	}
	var hash string
	var err error
	if matches {
		hash, err = auth.HashPassword(replacement)
	}
	s.releasePasswordWork()
	if err != nil {
		http.Error(w, "password change failed", http.StatusInternalServerError)
		return
	}
	if !matches {
		s.renderAccountFailure(w, r, user.Email)
		return
	}
	changed, err := s.store.ChangePassword(
		r.Context(), user.ID, user.PasswordHash, hash, auth.HashSessionToken(rawSession),
	)
	if err != nil {
		http.Error(w, "password change failed", http.StatusInternalServerError)
		return
	}
	if !changed {
		s.renderAccountFailure(w, r, user.Email)
		return
	}
	http.Redirect(w, r, "/account", http.StatusSeeOther)
}

func (s *Server) handleAccountDelete(w http.ResponseWriter, r *http.Request) {
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
	confirmedEmail := auth.NormalizeEmail(r.FormValue("confirm_email"))
	if len(current) > auth.MaxPasswordBytes ||
		confirmedEmail != user.Email {
		s.renderAccountFailure(w, r, user.Email)
		return
	}
	ip, allowed := s.reserveAccountMutation(r, user.ID)
	if !allowed {
		http.Error(w, accountErrorCopy, http.StatusTooManyRequests)
		return
	}
	if !s.acquirePasswordWork(r.Context()) {
		http.Error(w, accountErrorCopy, http.StatusTooManyRequests)
		return
	}
	matches := passwordMatches(user.PasswordHash, current)
	if matches {
		s.resetAccountMutation(ip, user.ID)
	}
	s.releasePasswordWork()
	if !matches {
		s.renderAccountFailure(w, r, user.Email)
		return
	}
	lease := s.flight.acquire(r.Context(), scrapeAllKey)
	if lease == nil {
		s.renderAccountFailure(w, r, user.Email)
		return
	}
	defer lease.release()
	deleted, err := s.store.DeleteSelf(
		r.Context(), user.ID, user.PasswordHash, auth.HashSessionToken(rawSession),
	)
	if err != nil {
		http.Error(w, "account deletion failed", http.StatusInternalServerError)
		return
	}
	if !deleted {
		s.renderAccountFailure(w, r, user.Email)
		return
	}
	cookie := s.sessionCookie("", time.Unix(0, 0))
	cookie.MaxAge = -1
	http.SetCookie(w, cookie)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (s *Server) reserveAccountMutation(r *http.Request, userID int64) (string, bool) {
	ip := s.clientIP(r)
	allowed := s.accountMutationIPLimiter.reserveIP(ip) &&
		s.accountMutationLimiter.reserveFailure(ip, strconv.FormatInt(userID, 10))
	return ip, allowed
}

func (s *Server) resetAccountMutation(ip string, userID int64) {
	s.accountMutationLimiter.reset(ip, strconv.FormatInt(userID, 10))
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
