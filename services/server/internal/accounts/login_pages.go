package accounts

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"html/template"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
)

const LoginTooManyFailures = "Too many failed login attempts. Please wait a few minutes and try again."

var errLoginOtherResponse = errors.New("login completed without credential verdict")

type loginCredentials struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// Decode the exact credential keys once. encoding/json's struct decoder accepts
// case aliases and repeated keys, so forwarding original bytes could verify a
// different username from the one whose failure reservation was admitted.
func parseLoginCredentials(raw []byte) (loginCredentials, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	start, err := decoder.Token()
	if err != nil || start != json.Delim('{') {
		return loginCredentials{}, platform.ErrInvalid
	}
	fields := map[string]string{}
	for decoder.More() {
		token, err := decoder.Token()
		key, ok := token.(string)
		if err != nil || !ok || (key != "username" && key != "password" && key != "email" && key != "name") {
			return loginCredentials{}, platform.ErrInvalid
		}
		if _, duplicate := fields[key]; duplicate {
			return loginCredentials{}, platform.ErrInvalid
		}
		var value string
		if decoder.Decode(&value) != nil {
			return loginCredentials{}, platform.ErrInvalid
		}
		fields[key] = value
	}
	if end, err := decoder.Token(); err != nil || end != json.Delim('}') {
		return loginCredentials{}, platform.ErrInvalid
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return loginCredentials{}, platform.ErrInvalid
	}
	// The pinned login handler ignores email/name. Validate their input types,
	// then omit them so JSON escaping cannot expand ignored metadata beyond its
	// bounded credential body and change an otherwise valid login outcome.
	return loginCredentials{Username: NormalizeLoginUsername(fields["username"]), Password: fields["password"]}, nil
}

type loginCapture struct {
	header   http.Header
	status   int
	body     bytes.Buffer
	overflow bool
}

func (c *loginCapture) Header() http.Header { return c.header }
func (c *loginCapture) WriteHeader(status int) {
	if c.status == 0 {
		c.status = status
	}
}
func (c *loginCapture) Write(raw []byte) (int, error) {
	if c.status == 0 {
		c.status = http.StatusOK
	}
	if c.body.Len()+len(raw) > 64<<10 {
		c.overflow = true
		return 0, io.ErrShortBuffer
	}
	return c.body.Write(raw)
}

var sourceLoginPage = template.Must(template.New("source-login").Parse(`<!doctype html><html lang="en"><meta charset="utf-8"><meta name="viewport" content="width=device-width"><title>Log in</title><main><h1>Log in</h1>{{if .Error}}<ul class="errorlist nonfield"><li>{{.Error}}</li></ul>{{end}}<form method="post"><input type="hidden" name="csrfmiddlewaretoken" value="{{.CSRF}}"><p><label for="id_username">Username:</label><input id="id_username" name="username" value="{{.Username}}" maxlength="150" autocomplete="username" required></p><p><label for="id_password">Password:</label><input id="id_password" name="password" type="password" maxlength="1024" autocomplete="current-password" required></p><input type="hidden" name="next" value="{{.Next}}"><button class="btn" type="submit">Log in</button></form><p class="muted">No account? <a href="/register/">Sign up</a>.</p><p class="muted">Account restricted? <a href="/account/restricted/">See why and contest the decision</a>.</p></main></html>`))

func loginNext(r *http.Request, value string) string {
	if value == "" || strings.ContainsAny(value, "\\\r\n") {
		return "/"
	}
	u, err := url.Parse(value)
	if err != nil || u.User != nil {
		return "/"
	}
	if u.IsAbs() {
		requireHTTPS := r.TLS != nil || r.URL.Scheme == "https"
		if origin, err := url.Parse(r.Header.Get("Origin")); err == nil && origin.Scheme == "https" {
			requireHTTPS = true
		}
		if u.Host != r.Host || u.Scheme != "https" && (requireHTTPS || u.Scheme != "http") {
			return "/"
		}
		value = u.RequestURI()
		if u.Fragment != "" {
			value += "#" + u.EscapedFragment()
		}
	}
	if !strings.HasPrefix(value, "/") || strings.HasPrefix(value, "//") {
		return "/"
	}
	return value
}

func (s *Service) loginHTML(w http.ResponseWriter, r *http.Request, username, next, message string) {
	csrf := s.Auth.EnsureCSRF(w, r)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
	w.WriteHeader(http.StatusOK)
	_ = sourceLoginPage.Execute(w, map[string]string{"CSRF": csrf, "Username": username, "Next": loginNext(r, next), "Error": message})
}

// LoginPOST retains the pinned password verifier, active-account gate and
// session/cookie protocol. Only credential failure admission and the source
// browser form response belong to this application wrapper.
func (s *Service) LoginPOST(w http.ResponseWriter, r *http.Request, browser bool) {
	if r.Method != http.MethodPost {
		platform.Error(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	if s.Auth == nil {
		platform.Error(w, http.StatusServiceUnavailable, "authentication unavailable")
		return
	}
	var raw []byte
	var username, next string
	if browser {
		r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
		if r.ParseForm() != nil {
			platform.Error(w, http.StatusBadRequest, "invalid login details")
			return
		}
		username = NormalizeLoginUsername(r.PostForm.Get("username"))
		next = r.PostForm.Get("next")
		r.Header.Set("X-CSRFToken", r.PostForm.Get("csrfmiddlewaretoken"))
		var err error
		raw, err = json.Marshal(loginCredentials{Username: username, Password: r.PostForm.Get("password")})
		if err != nil {
			platform.Error(w, http.StatusBadRequest, "invalid login details")
			return
		}
	} else {
		var err error
		raw, err = io.ReadAll(http.MaxBytesReader(w, r.Body, 16<<10))
		if err != nil {
			platform.Error(w, http.StatusBadRequest, "invalid login details")
			return
		}
		credentials, err := parseLoginCredentials(raw)
		if err != nil {
			platform.Error(w, http.StatusBadRequest, "invalid login details")
			return
		}
		username = credentials.Username
		raw, err = json.Marshal(credentials)
		if err != nil {
			platform.Error(w, http.StatusBadRequest, "invalid login details")
			return
		}
	}
	// The source middleware performs CSRF before password work and lockout.
	// The pinned handler repeats this check after receiving the private context.
	if s.Auth.CheckCSRF(r) != nil {
		platform.Error(w, http.StatusForbidden, "invalid request origin or CSRF token")
		return
	}
	if len(username) > 150 {
		if browser {
			s.loginHTML(w, r, "", next, "Please enter a correct username and password. Note that both fields may be case-sensitive.")
		} else {
			platform.Error(w, http.StatusBadRequest, "invalid login details")
		}
		return
	}
	reservation, err := s.reserveLogin(r.Context(), username, r.RemoteAddr)
	if errors.Is(err, ErrLoginFailureLimit) {
		if browser {
			s.loginHTML(w, r, username, next, LoginTooManyFailures)
		} else {
			platform.Error(w, http.StatusTooManyRequests, "try again later")
		}
		return
	}
	if err != nil {
		platform.Error(w, http.StatusServiceUnavailable, "authentication unavailable")
		return
	}
	capture := &loginCapture{header: make(http.Header)}
	_, err = s.runLoginReservation(r.Context(), reservation, func(ctx context.Context) (bool, error) {
		request := r.Clone(context.WithValue(ctx, reservedLoginContext{}, reservation))
		request.Body = io.NopCloser(bytes.NewReader(raw))
		request.ContentLength = int64(len(raw))
		request.Header = r.Header.Clone()
		request.Header.Set("Content-Type", "application/json")
		s.Auth.Login(capture, request)
		if capture.overflow {
			return false, io.ErrShortBuffer
		}
		if guardErr := reservation.guardError(); guardErr != nil {
			return false, guardErr
		}
		switch capture.status {
		case http.StatusUnauthorized:
			return false, nil
		case http.StatusOK, http.StatusFound:
			return true, nil
		default:
			return false, errLoginOtherResponse
		}
	})
	if err != nil && !errors.Is(err, errLoginOtherResponse) {
		platform.Error(w, http.StatusServiceUnavailable, "authentication unavailable")
		return
	}
	if browser && (capture.status == http.StatusUnauthorized || capture.status == http.StatusBadRequest) {
		s.loginHTML(w, r, username, next, "Please enter a correct username and password. Note that both fields may be case-sensitive.")
		return
	}
	for key, values := range capture.header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	if browser && (capture.status == http.StatusOK || capture.status == http.StatusFound) {
		http.Redirect(w, r, loginNext(r, next), http.StatusFound)
		return
	}
	if capture.status == 0 {
		platform.Error(w, http.StatusServiceUnavailable, "authentication unavailable")
		return
	}
	w.WriteHeader(capture.status)
	_, _ = w.Write(capture.body.Bytes())
}
