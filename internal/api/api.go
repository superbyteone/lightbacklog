// Package api is the REST adapter over the service layer. It contains no business rules:
// it authenticates the caller, decodes requests, calls the service and encodes results.
package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/superbyteone/lightbacklog/internal/ratelimit"
	"github.com/superbyteone/lightbacklog/internal/service"
)

func init() {
	// The distroless image has no system MIME table; the web manifest needs its own type.
	_ = mime.AddExtensionType(".webmanifest", "application/manifest+json")
}

const (
	sessionCookie = "lb_session"
	maxJSONBody   = 4 << 20
)

// Config controls transport-level behaviour.
type Config struct {
	// CookieSecure forces the Secure flag on session cookies; when false it is set
	// automatically for requests that arrived over HTTPS (directly or via X-Forwarded-Proto).
	CookieSecure bool
	// TrustProxy honours X-Real-IP / X-Forwarded-Proto from loopback and private-network peers.
	TrustProxy bool
	Static     fs.FS // built frontend, may be nil
	OpenAPI    []byte
	Version    string
	// MaxUploadBytes caps a single uploaded file (default 10 MiB).
	MaxUploadBytes int64
}

// API wires HTTP routes to the service.
type API struct {
	svc    *service.Service
	cfg    Config
	log    *slog.Logger
	mux    *http.ServeMux
	routes []string
	login  *ratelimit.FailLimiter
}

// New builds the HTTP handler for the whole application (API, files, static UI).
func New(svc *service.Service, cfg Config, log *slog.Logger) *API {
	a := &API{svc: svc, cfg: cfg, log: log, mux: http.NewServeMux(), login: ratelimit.NewFailLimiter(10, 10*time.Minute)}
	a.routeTable()
	a.mux.HandleFunc("/", a.static)
	return a
}

// Routes lists every registered API pattern (used by the OpenAPI parity test).
func (a *API) Routes() []string {
	out := append([]string{}, a.routes...)
	sort.Strings(out)
	return out
}

func (a *API) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h := w.Header()
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("X-Frame-Options", "DENY")
	h.Set("Referrer-Policy", "same-origin")
	h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
	h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data: blob:; style-src 'self' 'unsafe-inline'; script-src 'self'; connect-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
	a.mux.ServeHTTP(w, r)
}

type handlerFunc func(w http.ResponseWriter, r *http.Request, p service.Principal) error

func (a *API) route(pattern string, h http.HandlerFunc) {
	a.routes = append(a.routes, pattern)
	a.mux.HandleFunc(pattern, h)
}

// secured registers a route that requires authentication.
func (a *API) secured(pattern string, h handlerFunc) {
	a.route(pattern, func(w http.ResponseWriter, r *http.Request) {
		p, err := a.authenticate(r)
		if err == nil {
			r = r.WithContext(service.WithOrigin(r.Context(), r.Header.Get("X-LB-Client")))
			err = h(w, r, p)
		}
		if err != nil {
			a.fail(w, r, err)
		}
	})
}

// open registers a route that needs no authentication.
func (a *API) open(pattern string, h func(w http.ResponseWriter, r *http.Request) error) {
	a.route(pattern, func(w http.ResponseWriter, r *http.Request) {
		if err := h(w, r); err != nil {
			a.fail(w, r, err)
		}
	})
}

// --- authentication -------------------------------------------------------------------

func (a *API) authenticate(r *http.Request) (service.Principal, error) {
	if h := r.Header.Get("Authorization"); h != "" {
		scheme, tok, _ := strings.Cut(h, " ")
		if !strings.EqualFold(scheme, "bearer") || strings.TrimSpace(tok) == "" {
			return service.Principal{}, unauthorized("Authorization header must be 'Bearer <token>'")
		}
		return a.svc.AuthenticateToken(r.Context(), strings.TrimSpace(tok))
	}
	c, err := r.Cookie(sessionCookie)
	if err != nil || c.Value == "" {
		return service.Principal{}, unauthorized("authentication required: log in or send an API token as 'Authorization: Bearer <token>'")
	}
	if err := a.checkCSRF(r); err != nil {
		return service.Principal{}, err
	}
	return a.svc.AuthenticateSession(r.Context(), c.Value)
}

// checkCSRF protects cookie-authenticated mutations: the browser must send a custom header
// (which cross-site requests cannot add without a CORS preflight, and we send no CORS
// headers) and, when present, Fetch Metadata must say same-origin.
func (a *API) checkCSRF(r *http.Request) error {
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return nil
	}
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
		return &service.Error{Status: http.StatusForbidden, Code: "csrf_rejected", Message: "cross-site request rejected"}
	}
	if r.Header.Get("X-LB-CSRF") == "" {
		return &service.Error{Status: http.StatusForbidden, Code: "csrf_rejected", Message: "missing X-LB-CSRF header"}
	}
	return nil
}

func unauthorized(msg string) error {
	return &service.Error{Status: http.StatusUnauthorized, Code: "unauthorized", Message: msg}
}

func (a *API) clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if a.cfg.TrustProxy {
		if ip := net.ParseIP(host); ip != nil && (ip.IsLoopback() || ip.IsPrivate()) {
			if v := strings.TrimSpace(r.Header.Get("X-Real-IP")); v != "" {
				return v
			}
		}
	}
	return host
}

func (a *API) isHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	if a.cfg.TrustProxy {
		if ip, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
			if pip := net.ParseIP(ip); pip != nil && (pip.IsLoopback() || pip.IsPrivate()) {
				return r.Header.Get("X-Forwarded-Proto") == "https"
			}
		}
	}
	return false
}

// --- responses ------------------------------------------------------------------------

type envelope struct {
	Data       any    `json:"data"`
	NextCursor string `json:"next_cursor,omitempty"`
	Total      *int   `json:"total,omitempty"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func ok(w http.ResponseWriter, data any)      { writeJSON(w, http.StatusOK, envelope{Data: data}) }
func created(w http.ResponseWriter, data any) { writeJSON(w, http.StatusCreated, envelope{Data: data}) }

type problem struct {
	Type    string               `json:"type"`
	Title   string               `json:"title"`
	Status  int                  `json:"status"`
	Code    string               `json:"code"`
	Detail  string               `json:"detail"`
	Errors  []service.FieldError `json:"errors,omitempty"`
	Current any                  `json:"current,omitempty"`
}

func (a *API) fail(w http.ResponseWriter, r *http.Request, err error) {
	var se *service.Error
	if !errors.As(err, &se) {
		if errors.Is(err, context.Canceled) {
			return
		}
		var mb *http.MaxBytesError
		if errors.As(err, &mb) {
			se = &service.Error{Status: http.StatusRequestEntityTooLarge, Code: "request_too_large", Message: "request body is too large"}
		} else {
			a.log.Error("internal error", "method", r.Method, "path", r.URL.Path, "err", err)
			se = &service.Error{Status: http.StatusInternalServerError, Code: "internal", Message: "internal server error"}
		}
	}
	w.Header().Set("Content-Type", "application/problem+json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if se.Status == http.StatusUnauthorized {
		w.Header().Set("WWW-Authenticate", `Bearer realm="lightbacklog"`)
	}
	w.WriteHeader(se.Status)
	_ = json.NewEncoder(w).Encode(problem{
		Type: "urn:lightbacklog:error:" + se.Code, Title: http.StatusText(se.Status), Status: se.Status,
		Code: se.Code, Detail: se.Message, Errors: se.Fields, Current: se.Current,
	})
}

func badRequest(code, msg string) error {
	return &service.Error{Status: http.StatusBadRequest, Code: code, Message: msg}
}

// decode reads a JSON body strictly: unknown fields are errors so typos never silently vanish.
func decode(w http.ResponseWriter, r *http.Request, v any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxJSONBody)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		var mb *http.MaxBytesError
		if errors.As(err, &mb) {
			return err
		}
		if errors.Is(err, io.EOF) {
			return badRequest("invalid_json", "request body is empty; expected a JSON object")
		}
		return badRequest("invalid_json", "invalid JSON body: "+err.Error())
	}
	if dec.More() {
		return badRequest("invalid_json", "unexpected data after the JSON object")
	}
	return nil
}

// --- idempotency ----------------------------------------------------------------------

// idempotent runs a creating handler at most once per Idempotency-Key.
func (a *API) idempotent(w http.ResponseWriter, r *http.Request, p service.Principal, do func(body []byte) (int, any, error)) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxJSONBody)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return err
	}
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" {
		status, v, err := do(body)
		if err != nil {
			return err
		}
		writeJSON(w, status, envelope{Data: v})
		return nil
	}
	if len(key) > 200 {
		return badRequest("invalid_idempotency_key", "Idempotency-Key must be at most 200 characters")
	}
	sum := sha256.Sum256(append([]byte(r.Method+" "+r.URL.Path+"\n"), body...))
	hash := hex.EncodeToString(sum[:])
	if stored, err := a.svc.IdempotencyLookup(r.Context(), p.UserID, key, hash); err != nil {
		return err
	} else if stored != nil {
		w.Header().Set("Idempotent-Replay", "true")
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(stored.Status)
		_, _ = w.Write(stored.Body)
		return nil
	}
	status, v, err := do(body)
	if err != nil {
		return err
	}
	var buf bytes.Buffer
	_ = json.NewEncoder(&buf).Encode(envelope{Data: v})
	a.svc.IdempotencyStore(r.Context(), p.UserID, key, hash, status, buf.Bytes())
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write(buf.Bytes())
	return nil
}

func decodeBytes(body []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		if errors.Is(err, io.EOF) {
			return badRequest("invalid_json", "request body is empty; expected a JSON object")
		}
		return badRequest("invalid_json", "invalid JSON body: "+err.Error())
	}
	return nil
}

// --- static frontend ------------------------------------------------------------------

func (a *API) static(w http.ResponseWriter, r *http.Request) {
	if a.cfg.Static == nil || (r.Method != http.MethodGet && r.Method != http.MethodHead) {
		a.fail(w, r, &service.Error{Status: http.StatusNotFound, Code: "not_found", Message: "no such route"})
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/") {
		a.fail(w, r, &service.Error{Status: http.StatusNotFound, Code: "not_found", Message: "no such API route"})
		return
	}
	name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
	if name == "" {
		name = "index.html"
	}
	f, err := a.cfg.Static.Open(name)
	if err != nil || isDir(f) {
		if f != nil {
			f.Close()
		}
		if path.Ext(name) != "" { // a missing asset is a 404, not the app shell
			http.NotFound(w, r)
			return
		}
		name = "index.html"
		if f, err = a.cfg.Static.Open(name); err != nil {
			http.NotFound(w, r)
			return
		}
	}
	defer f.Close()
	if strings.HasPrefix(name, "assets/") {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "no-cache")
	}
	st, _ := f.Stat()
	if rs, ok := f.(io.ReadSeeker); ok {
		http.ServeContent(w, r, name, st.ModTime(), rs)
		return
	}
	data, _ := io.ReadAll(f)
	http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(data))
}

func isDir(f fs.File) bool {
	st, err := f.Stat()
	return err != nil || st.IsDir()
}
