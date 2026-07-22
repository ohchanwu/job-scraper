package server

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"net/http"
	"time"
)

const (
	csrfCookieName = "jobcron_csrf"
	csrfHeaderName = "X-CSRF-Token"
	csrfFieldName  = "csrf_token"
	csrfCookieTTL  = 30 * 24 * time.Hour
)

func newCSRFSecret() []byte {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		panic("server: generate csrf secret: " + err.Error())
	}
	return secret
}

func (s *Server) csrfProtect(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !unsafeMethod(r.Method) {
			s.ensureCSRFCookie(w, r)
			next.ServeHTTP(w, r)
			return
		}
		if !s.validCSRF(r) {
			http.Error(w, "invalid csrf token", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func unsafeMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace:
		return false
	default:
		return true
	}
}

func (s *Server) csrfTokenForRequest(w http.ResponseWriter, r *http.Request) string {
	cookieValue := s.ensureCSRFCookie(w, r)
	sessionValue := ""
	if cookie, err := r.Cookie(sessionCookieName); err == nil {
		sessionValue = cookie.Value
	}
	return s.csrfToken(cookieValue, sessionValue)
}

func (s *Server) ensureCSRFCookie(w http.ResponseWriter, r *http.Request) string {
	if cookie, err := r.Cookie(csrfCookieName); err == nil && cookie.Value != "" {
		return cookie.Value
	}
	value := randomToken()
	cookie := &http.Cookie{
		Name:     csrfCookieName,
		Value:    value,
		Path:     "/",
		Expires:  time.Now().Add(csrfCookieTTL).UTC(),
		HttpOnly: true,
		Secure:   s.productionMode,
		SameSite: http.SameSiteLaxMode,
	}
	http.SetCookie(w, cookie)
	r.AddCookie(cookie)
	return value
}

func randomToken() string {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		panic("server: generate random token: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(buf)
}

func (s *Server) csrfToken(cookieValue, sessionValue string) string {
	mac := hmac.New(sha256.New, s.csrfSecret)
	mac.Write([]byte(cookieValue))
	mac.Write([]byte{0})
	mac.Write([]byte(sessionValue))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (s *Server) validCSRF(r *http.Request) bool {
	cookie, err := r.Cookie(csrfCookieName)
	if err != nil || cookie.Value == "" {
		return false
	}
	got := r.Header.Get(csrfHeaderName)
	if got == "" {
		if err := r.ParseForm(); err != nil {
			return false
		}
		got = r.FormValue(csrfFieldName)
	}
	if got == "" {
		return false
	}
	sessionValue := ""
	if sessionCookie, err := r.Cookie(sessionCookieName); err == nil {
		sessionValue = sessionCookie.Value
	}
	want := s.csrfToken(cookie.Value, sessionValue)
	if len(got) != len(want) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

func (s *Server) renderWithRequest(w http.ResponseWriter, r *http.Request, name string, data any) {
	token := s.csrfTokenForRequest(w, r)
	s.render(w, name, withCSRFToken(data, token))
}

func withCSRFToken(data any, token string) any {
	switch v := data.(type) {
	case loginPage:
		v.CSRFToken = token
		return v
	case signupPage:
		v.CSRFToken = token
		return v
	case briefing:
		v.CSRFToken = token
		return v
	case profileForm:
		v.CSRFToken = token
		return v
	case archiveView:
		v.CSRFToken = token
		return v
	case bookmarksView:
		v.CSRFToken = token
		return v
	case hiddenView:
		v.CSRFToken = token
		return v
	default:
		return data
	}
}
