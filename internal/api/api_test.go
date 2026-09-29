package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/superbyteone/lightbacklog/internal/files"
	"github.com/superbyteone/lightbacklog/internal/service"
	"github.com/superbyteone/lightbacklog/internal/store"
)

type harness struct {
	t   *testing.T
	srv *httptest.Server
	svc *service.Service
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(context.Background(), filepath.Join(dir, "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	fs, _ := files.NewStore(filepath.Join(dir, "uploads"))
	svc := service.New(db, fs, service.Config{MaxUploadBytes: 1 << 20})
	a := New(svc, Config{MaxUploadBytes: 1 << 20, OpenAPI: OpenAPISpec, Version: "test"}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	srv := httptest.NewServer(a)
	t.Cleanup(srv.Close)
	return &harness{t: t, srv: srv, svc: svc}
}

func (h *harness) mkUser(name string, admin bool) {
	h.t.Helper()
	if _, err := h.svc.CreateUser(context.Background(), service.CreateUserInput{Username: name, Password: "correct horse battery", IsAdmin: admin}); err != nil {
		h.t.Fatal(err)
	}
}

type client struct {
	h      *harness
	cookie *http.Cookie
	token  string
}

// session logs in through the real endpoint and returns a cookie-authenticated client.
func (h *harness) session(name string) *client {
	h.t.Helper()
	c := &client{h: h}
	resp := c.do("POST", "/api/v1/auth/login", map[string]string{"username": name, "password": "correct horse battery"}, "X-LB-CSRF", "1")
	if resp.status != 200 {
		h.t.Fatalf("login failed: %d %s", resp.status, resp.raw)
	}
	for _, ck := range resp.cookies {
		if ck.Name == sessionCookie {
			c.cookie = ck
		}
	}
	if c.cookie == nil {
		h.t.Fatal("no session cookie")
	}
	return c
}

func (c *client) withToken(secret string) *client { return &client{h: c.h, token: secret} }

type response struct {
	status  int
	raw     []byte
	header  http.Header
	cookies []*http.Cookie
	body    map[string]any
}

func (r response) data() map[string]any { m, _ := r.body["data"].(map[string]any); return m }
func (r response) list() []any          { l, _ := r.body["data"].([]any); return l }

func (c *client) do(method, path string, body any, headers ...string) response {
	c.h.t.Helper()
	var rd io.Reader
	switch b := body.(type) {
	case nil:
	case []byte:
		rd = bytes.NewReader(b)
	case string:
		rd = strings.NewReader(b)
	default:
		buf, _ := json.Marshal(b)
		rd = bytes.NewReader(buf)
	}
	req, _ := http.NewRequest(method, c.h.srv.URL+path, rd)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.cookie != nil {
		req.AddCookie(c.cookie)
		if method != "GET" {
			req.Header.Set("X-LB-CSRF", "1")
		}
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.h.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	out := response{status: resp.StatusCode, raw: raw, header: resp.Header, cookies: resp.Cookies()}
	_ = json.Unmarshal(raw, &out.body)
	return out
}

func (r response) str(path ...string) string {
	var cur any = r.body
	for _, k := range path {
		m, ok := cur.(map[string]any)
		if !ok {
			return ""
		}
		cur = m[k]
	}
	s, _ := cur.(string)
	return s
}

func TestAuthCSRFAndProblemFormat(t *testing.T) {
	h := newHarness(t)
	h.mkUser("alice", true)
	anon := &client{h: h}

	r := anon.do("GET", "/api/v1/projects", nil)
	if r.status != 401 || r.str("code") != "unauthorized" || !strings.HasPrefix(r.header.Get("Content-Type"), "application/problem+json") {
		t.Fatalf("anon request: %d %s", r.status, r.raw)
	}
	if r.header.Get("WWW-Authenticate") == "" {
		t.Fatal("401 must carry WWW-Authenticate")
	}
	bad := anon.do("POST", "/api/v1/auth/login", map[string]string{"username": "alice", "password": "nope nope nope"}, "X-LB-CSRF", "1")
	if bad.status != 401 || bad.str("code") != "invalid_credentials" {
		t.Fatalf("bad login: %d %s", bad.status, bad.raw)
	}
	// Login without the CSRF header is refused even with right credentials.
	noCSRF := anon.do("POST", "/api/v1/auth/login", map[string]string{"username": "alice", "password": "correct horse battery"})
	if noCSRF.status != 403 || noCSRF.str("code") != "csrf_rejected" {
		t.Fatalf("csrf: %d %s", noCSRF.status, noCSRF.raw)
	}

	c := h.session("alice")
	if !c.cookie.HttpOnly || c.cookie.SameSite != http.SameSiteLaxMode {
		t.Fatalf("cookie flags wrong: %+v", c.cookie)
	}
	me := c.do("GET", "/api/v1/me", nil)
	if me.status != 200 || me.data()["username"] != "alice" || me.data()["auth"] != "session" {
		t.Fatalf("me: %s", me.raw)
	}
	// Cookie-authenticated writes need the CSRF header, and cross-site fetches are rejected.
	req, _ := http.NewRequest("POST", h.srv.URL+"/api/v1/projects", strings.NewReader(`{"name":"X"}`))
	req.AddCookie(c.cookie)
	req.Header.Set("Content-Type", "application/json")
	resp, _ := http.DefaultClient.Do(req)
	if resp.StatusCode != 403 {
		t.Fatalf("cookie POST without CSRF header got %d", resp.StatusCode)
	}
	req.Header.Set("X-LB-CSRF", "1")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	req.Body = io.NopCloser(strings.NewReader(`{"name":"X"}`))
	resp, _ = http.DefaultClient.Do(req)
	if resp.StatusCode != 403 {
		t.Fatalf("cross-site POST got %d", resp.StatusCode)
	}

	// Strict JSON: unknown fields are reported, not ignored.
	r = c.do("POST", "/api/v1/projects", map[string]any{"name": "P", "colour": "#fff"})
	if r.status != 400 || r.str("code") != "invalid_json" {
		t.Fatalf("unknown field: %d %s", r.status, r.raw)
	}
	r = c.do("POST", "/api/v1/projects", map[string]any{"name": ""})
	if r.status != 422 || r.str("code") != "validation_failed" {
		t.Fatalf("validation: %d %s", r.status, r.raw)
	}
	errs, _ := r.body["errors"].([]any)
	if len(errs) != 1 || errs[0].(map[string]any)["field"] != "name" {
		t.Fatalf("field errors missing: %s", r.raw)
	}
	if out := c.do("POST", "/api/v1/auth/logout", nil); out.status != 204 {
		t.Fatalf("logout %d", out.status)
	}
	if r := c.do("GET", "/api/v1/me", nil); r.status != 401 {
		t.Fatalf("session still valid after logout: %d", r.status)
	}
}

func TestLoginRateLimit(t *testing.T) {
	h := newHarness(t)
	h.mkUser("alice", true)
	anon := &client{h: h}
	var last response
	for i := 0; i < 12; i++ {
		last = anon.do("POST", "/api/v1/auth/login", map[string]string{"username": "alice", "password": "wrong password!!"}, "X-LB-CSRF", "1")
	}
	if last.status != 429 || last.header.Get("Retry-After") == "" {
		t.Fatalf("expected throttling, got %d", last.status)
	}
	// Correct credentials are also refused while throttled.
	if r := anon.do("POST", "/api/v1/auth/login", map[string]string{"username": "alice", "password": "correct horse battery"}, "X-LB-CSRF", "1"); r.status != 429 {
		t.Fatalf("throttle bypassed: %d", r.status)
	}
}

func TestAgentWorkflowOverREST(t *testing.T) {
	h := newHarness(t)
	h.mkUser("alice", true)
	h.mkUser("bob", false)
	alice, bob := h.session("alice"), h.session("bob")

	proj := alice.do("POST", "/api/v1/projects", map[string]any{"name": "Agent Playground", "key": "AGT"})
	if proj.status != 201 {
		t.Fatalf("create project: %s", proj.raw)
	}
	tok := alice.do("POST", "/api/v1/tokens", map[string]any{"name": "codex", "scope": "write", "projects": []string{"AGT"}})
	secret := tok.str("data", "secret")
	if tok.status != 201 || !strings.HasPrefix(secret, "lb_") {
		t.Fatalf("token: %s", tok.raw)
	}
	if list := alice.do("GET", "/api/v1/tokens", nil); strings.Contains(string(list.raw), secret) {
		t.Fatal("token secret leaked in listing")
	}
	agent := alice.withToken(secret)

	meta := agent.do("GET", "/api/v1/meta", nil)
	if len(meta.data()["statuses"].([]any)) != 4 || len(meta.data()["priorities"].([]any)) != 3 {
		t.Fatalf("meta: %s", meta.raw)
	}

	// Create with an Idempotency-Key twice: one task, replay flagged.
	body := map[string]any{"project": "AGT", "title": "Investigate flaky test", "labels": []string{"ci"}, "external_ref": "issue-42"}
	r1 := agent.do("POST", "/api/v1/tasks", body, "Idempotency-Key", "abc-123")
	r2 := agent.do("POST", "/api/v1/tasks", body, "Idempotency-Key", "abc-123")
	if r1.status != 201 || r2.status != 201 || r2.header.Get("Idempotent-Replay") != "true" || r1.str("data", "id") != r2.str("data", "id") {
		t.Fatalf("idempotent create: %d/%d %s", r1.status, r2.status, r2.raw)
	}
	if r := agent.do("POST", "/api/v1/tasks", map[string]any{"project": "AGT", "title": "different"}, "Idempotency-Key", "abc-123"); r.status != 422 || r.str("code") != "idempotency_key_reuse" {
		t.Fatalf("key reuse: %d %s", r.status, r.raw)
	}
	// Without a key, external_ref still prevents duplicates and reports 200.
	r3 := agent.do("POST", "/api/v1/tasks", body)
	if r3.status != 200 || r3.header.Get("X-Existing-Task") != "true" || r3.str("data", "id") != r1.str("data", "id") {
		t.Fatalf("external_ref dedupe: %d %s", r3.status, r3.raw)
	}
	id := r1.str("data", "id")

	// ETag / If-Match optimistic concurrency.
	got := agent.do("GET", "/api/v1/tasks/AGT-1", nil)
	if got.header.Get("ETag") != `"1"` || got.str("data", "ref") != "AGT-1" {
		t.Fatalf("get by ref: %s / %v", got.raw, got.header)
	}
	upd := agent.do("PATCH", "/api/v1/tasks/"+id, map[string]any{"status": "in progress", "description": "## Notes\n- [ ] repro"}, "If-Match", `"1"`)
	if upd.status != 200 || upd.str("data", "status") != "in_progress" || upd.header.Get("ETag") != `"2"` {
		t.Fatalf("patch: %d %s", upd.status, upd.raw)
	}
	stale := agent.do("PATCH", "/api/v1/tasks/"+id, map[string]any{"title": "stale"}, "If-Match", `"1"`)
	if stale.status != 409 || stale.str("code") != "version_conflict" || stale.body["current"] == nil {
		t.Fatalf("stale write: %d %s", stale.status, stale.raw)
	}
	// Explicit null clears the due date; absent leaves it.
	agent.do("PATCH", "/api/v1/tasks/"+id, `{"due_date":"2026-12-01"}`)
	cleared := agent.do("PATCH", "/api/v1/tasks/"+id, `{"due_date":null}`)
	if cleared.data()["due_date"] != nil {
		t.Fatalf("null did not clear: %s", cleared.raw)
	}

	// Lists: descriptions omitted by default, total optional, project sub-route works.
	list := agent.do("GET", "/api/v1/tasks?project=AGT&status=in_progress&label=CI&include_total=true", nil)
	items := list.list()
	if len(items) != 1 || items[0].(map[string]any)["description"] != nil || list.body["total"].(float64) != 1 {
		t.Fatalf("list: %s", list.raw)
	}
	withDesc := agent.do("GET", "/api/v1/projects/AGT/tasks?include_description=true", nil)
	if withDesc.list()[0].(map[string]any)["description"] == nil {
		t.Fatalf("include_description ignored: %s", withDesc.raw)
	}
	if r := agent.do("GET", "/api/v1/tasks?limit=abc", nil); r.status != 400 {
		t.Fatalf("bad limit: %d", r.status)
	}
	if r := agent.do("GET", "/api/v1/tasks?status=bogus", nil); r.status != 422 {
		t.Fatalf("bad status filter: %d", r.status)
	}

	// The agent's token cannot see another project, nor manage tokens; Bob sees nothing of Alice's.
	other := alice.do("POST", "/api/v1/projects", map[string]any{"name": "Private"})
	if r := agent.do("GET", "/api/v1/projects/"+other.str("data", "id"), nil); r.status != 404 {
		t.Fatalf("scoped token saw other project: %d", r.status)
	}
	if r := agent.do("POST", "/api/v1/tokens", map[string]any{"name": "x"}); r.status != 403 {
		t.Fatalf("token minted a token: %d", r.status)
	}
	if r := bob.do("GET", "/api/v1/tasks/"+id, nil); r.status != 404 {
		t.Fatalf("bob read alice's task: %d", r.status)
	}
	if r := bob.do("GET", "/api/v1/tasks", nil); len(r.list()) != 0 {
		t.Fatalf("bob listing leaked: %s", r.raw)
	}

	// Move and delete.
	mv := alice.do("POST", "/api/v1/tasks/"+id+"/move", map[string]any{"project": other.str("data", "key")})
	if mv.status != 200 || mv.str("data", "project", "id") != other.str("data", "id") {
		t.Fatalf("move: %s", mv.raw)
	}
	if r := agent.do("DELETE", "/api/v1/tasks/"+id, nil); r.status != 404 { // moved out of the token's scope
		t.Fatalf("agent deleted out-of-scope task: %d", r.status)
	}
	if r := alice.do("DELETE", "/api/v1/tasks/"+id, nil); r.status != 204 {
		t.Fatalf("delete: %d", r.status)
	}
	// Revoking the token takes effect at once.
	tokID := tok.str("data", "id")
	if r := alice.do("DELETE", "/api/v1/tokens/"+tokID, nil); r.status != 204 {
		t.Fatalf("revoke: %d", r.status)
	}
	if r := agent.do("GET", "/api/v1/meta", nil); r.status != 401 {
		t.Fatalf("revoked token accepted: %d", r.status)
	}
}

func multipartBody(t *testing.T, fields map[string]string, filename string, content []byte) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for k, v := range fields {
		mw.WriteField(k, v)
	}
	hdr := textproto.MIMEHeader{}
	hdr.Set("Content-Disposition", `form-data; name="file"; filename="`+filename+`"`)
	hdr.Set("Content-Type", "text/html") // client-claimed type is ignored
	pw, _ := mw.CreatePart(hdr)
	pw.Write(content)
	mw.Close()
	return &buf, mw.FormDataContentType()
}

func TestTokenExpiryAndRotationOverREST(t *testing.T) {
	h := newHarness(t)
	h.mkUser("alice", true)
	alice := h.session("alice")

	tok := alice.do("POST", "/api/v1/tokens", map[string]any{"name": "codex", "scope": "write", "expires_in_days": 30})
	if tok.status != 201 || tok.data()["expires_at"] == nil {
		t.Fatalf("create with expiry: %s", tok.raw)
	}
	secret := tok.str("data", "secret")
	id := tok.str("data", "id")
	agent := alice.withToken(secret)
	if r := agent.do("GET", "/api/v1/meta", nil); r.status != 200 {
		t.Fatalf("fresh token should work: %d", r.status)
	}

	rot := alice.do("POST", "/api/v1/tokens/"+id+"/rotate", nil)
	if rot.status != 201 {
		t.Fatalf("rotate: %s", rot.raw)
	}
	newSecret := rot.str("data", "secret")
	if newSecret == "" || newSecret == secret || rot.data()["expires_at"] == nil {
		t.Fatalf("rotate did not carry expiry or return a fresh secret: %s", rot.raw)
	}
	if r := agent.do("GET", "/api/v1/meta", nil); r.status != http.StatusUnauthorized {
		t.Fatalf("old secret should stop working after rotation: %d", r.status)
	}
	if r := alice.withToken(newSecret).do("GET", "/api/v1/meta", nil); r.status != 200 {
		t.Fatalf("rotated secret should work: %d", r.status)
	}
}

func TestUploadsAndFileServing(t *testing.T) {
	h := newHarness(t)
	h.mkUser("alice", true)
	h.mkUser("bob", false)
	alice, bob := h.session("alice"), h.session("bob")
	alice.do("POST", "/api/v1/projects", map[string]any{"name": "Docs", "key": "DOC"})
	tk := alice.do("POST", "/api/v1/tasks", map[string]any{"project": "DOC", "title": "screenshots"})
	id := tk.str("data", "id")
	png := append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{1}, 100)...)

	upload := func(c *client, path string, fields map[string]string, name string, content []byte) response {
		buf, ct := multipartBody(t, fields, name, content)
		return c.do("POST", path, buf.Bytes(), "Content-Type", ct)
	}
	up := upload(alice, "/api/v1/tasks/"+id+"/attachments", nil, "shot.png", png)
	if up.status != 201 || up.str("data", "mime") != "image/png" || !strings.HasPrefix(up.str("data", "markdown"), "![shot.png](/files/") {
		t.Fatalf("upload: %d %s", up.status, up.raw)
	}
	fileURL := up.str("data", "url")

	f := alice.do("GET", fileURL, nil)
	if f.status != 200 || f.header.Get("Content-Type") != "image/png" || f.header.Get("X-Content-Type-Options") != "nosniff" ||
		!strings.HasPrefix(f.header.Get("Content-Disposition"), "inline") || !strings.Contains(f.header.Get("Content-Security-Policy"), "sandbox") {
		t.Fatalf("file headers: %d %v", f.status, f.header)
	}
	if !bytes.Equal(f.raw, png) {
		t.Fatal("file content mismatch")
	}
	if r := bob.do("GET", fileURL, nil); r.status != 404 {
		t.Fatalf("bob fetched alice's file: %d", r.status)
	}
	if r := (&client{h: h}).do("GET", fileURL, nil); r.status != 401 {
		t.Fatalf("anonymous fetched file: %d", r.status)
	}
	// Text downloads as attachment, served as text/plain despite the html claim.
	txt := upload(alice, "/api/v1/tasks/"+id+"/attachments", nil, "log.html", []byte("just a log"))
	got := alice.do("GET", txt.str("data", "url"), nil)
	if got.header.Get("Content-Type") != "text/plain; charset=utf-8" || !strings.HasPrefix(got.header.Get("Content-Disposition"), "attachment") {
		t.Fatalf("text served unsafely: %v", got.header)
	}
	if r := upload(alice, "/api/v1/tasks/"+id+"/attachments", nil, "evil.svg", []byte(`<svg onload="alert(1)"/>`)); r.status != 415 {
		t.Fatalf("svg accepted: %d %s", r.status, r.raw)
	}
	if r := upload(alice, "/api/v1/tasks/"+id+"/attachments", nil, "big.txt", bytes.Repeat([]byte("a"), 1<<20+5)); r.status != 413 {
		t.Fatalf("oversize: %d", r.status)
	}
	if r := upload(bob, "/api/v1/tasks/"+id+"/attachments", nil, "x.txt", []byte("hi")); r.status != 404 {
		t.Fatalf("bob uploaded to alice's task: %d", r.status)
	}
	// Pre-task upload by project, then reference from a new task's description.
	pre := upload(alice, "/api/v1/uploads", map[string]string{"project": "DOC"}, "paste.png", png)
	if pre.status != 201 || pre.data()["task_id"] != nil {
		t.Fatalf("project upload: %d %s", pre.status, pre.raw)
	}
	nt := alice.do("POST", "/api/v1/tasks", map[string]any{"project": "DOC", "title": "with paste", "description": pre.str("data", "markdown")})
	list := alice.do("GET", "/api/v1/tasks/"+nt.str("data", "id")+"/attachments", nil)
	if len(list.list()) != 1 {
		t.Fatalf("attachment not linked: %s", list.raw)
	}
	// Raw-body upload for agents (curl --data-binary).
	raw := alice.do("POST", "/api/v1/tasks/"+id+"/attachments?filename=notes.txt", []byte("raw body"), "Content-Type", "application/octet-stream")
	if raw.status != 201 || raw.str("data", "filename") != "notes.txt" {
		t.Fatalf("raw upload: %d %s", raw.status, raw.raw)
	}
}

func TestStaticAndHealth(t *testing.T) {
	h := newHarness(t)
	anon := &client{h: h}
	if r := anon.do("GET", "/healthz", nil); r.status != 200 || r.str("status") != "ok" {
		t.Fatalf("healthz %d", r.status)
	}
	if r := anon.do("GET", "/api/v1/nope", nil); r.status != 404 {
		t.Fatalf("unknown api route %d", r.status)
	}
	if r := anon.do("GET", "/api/v1/openapi.yaml", nil); r.status != 200 || !strings.Contains(string(r.raw), "openapi:") {
		t.Fatalf("openapi %d", r.status)
	}
	if r := anon.do("GET", "/api/v1/tasks", nil); r.header.Get("Content-Security-Policy") == "" {
		t.Fatal("missing CSP header")
	}
}

func TestEventStreamDeliversOnlyVisibleChanges(t *testing.T) {
	h := newHarness(t)
	h.mkUser("alice", true)
	h.mkUser("bob", false)
	alice, bob := h.session("alice"), h.session("bob")
	alice.do("POST", "/api/v1/projects", map[string]any{"name": "Stream", "key": "STR"})

	open := func(c *client) (<-chan string, func()) {
		req, _ := http.NewRequest("GET", h.srv.URL+"/api/v1/events", nil)
		req.AddCookie(c.cookie)
		resp, err := http.DefaultClient.Do(req)
		if err != nil || resp.StatusCode != 200 || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
			t.Fatalf("event stream: %v %v", err, resp)
		}
		lines := make(chan string, 64)
		go func() {
			defer close(lines)
			buf := make([]byte, 4096)
			var acc string
			for {
				n, err := resp.Body.Read(buf)
				acc += string(buf[:n])
				for {
					i := strings.Index(acc, "\n\n")
					if i < 0 {
						break
					}
					lines <- acc[:i]
					acc = acc[i+2:]
				}
				if err != nil {
					return
				}
			}
		}()
		return lines, func() { resp.Body.Close() }
	}
	aliceEv, stopA := open(alice)
	defer stopA()
	bobEv, stopB := open(bob)
	defer stopB()
	if first := <-aliceEv; !strings.Contains(first, "connected") {
		t.Fatalf("expected connection preamble, got %q", first)
	}
	<-bobEv

	r := alice.do("POST", "/api/v1/tasks", map[string]any{"project": "STR", "title": "streamed"}, "X-LB-Client", "tab-9")
	if r.status != 201 {
		t.Fatalf("create: %s", r.raw)
	}
	select {
	case ev := <-aliceEv:
		if !strings.Contains(ev, "event: task.created") || !strings.Contains(ev, `"origin":"tab-9"`) || !strings.Contains(ev, r.str("data", "id")) {
			t.Fatalf("unexpected event %q", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no event received")
	}
	select {
	case ev := <-bobEv:
		t.Fatalf("bob must not see alice's project events, got %q", ev)
	case <-time.After(300 * time.Millisecond):
	}
	if r := (&client{h: h}).do("GET", "/api/v1/events", nil); r.status != 401 {
		t.Fatalf("anonymous event stream: %d", r.status)
	}
}

func TestSequenceOverREST(t *testing.T) {
	h := newHarness(t)
	h.mkUser("alice", true)
	c := h.session("alice")
	c.do("POST", "/api/v1/projects", map[string]any{"name": "Seq", "key": "SEQ"})
	a := c.do("POST", "/api/v1/tasks", map[string]any{"project": "SEQ", "title": "a", "sequence": 2})
	b := c.do("POST", "/api/v1/tasks", map[string]any{"project": "SEQ", "title": "b"})
	if a.data()["sequence"].(float64) != 2 || b.data()["sequence"] != nil {
		t.Fatalf("sequence in responses: %s / %s", a.raw, b.raw)
	}
	if r := c.do("PATCH", "/api/v1/tasks/"+b.str("data", "id"), `{"sequence":1}`); r.data()["sequence"].(float64) != 1 {
		t.Fatalf("patch: %s", r.raw)
	}
	list := c.do("GET", "/api/v1/tasks?project=SEQ&sort=sequence", nil)
	if list.list()[0].(map[string]any)["title"] != "b" {
		t.Fatalf("sort=sequence: %s", list.raw)
	}
	if r := c.do("PATCH", "/api/v1/tasks/"+b.str("data", "id"), `{"sequence":null}`); r.data()["sequence"] != nil {
		t.Fatalf("null should clear: %s", r.raw)
	}
	if r := c.do("PATCH", "/api/v1/tasks/"+b.str("data", "id"), `{"sequence":-3}`); r.status != 422 || r.str("code") != "validation_failed" {
		t.Fatalf("negative: %d %s", r.status, r.raw)
	}
	if r := c.do("PATCH", "/api/v1/tasks/"+b.str("data", "id"), `{"sequence":1.5}`); r.status != 400 {
		t.Fatalf("fractional sequence should be a 400 JSON error, got %d %s", r.status, r.raw)
	}
}

func TestPreferencesOverREST(t *testing.T) {
	h := newHarness(t)
	h.mkUser("alice", true)
	c := h.session("alice")
	if r := c.do("GET", "/api/v1/me/preferences", nil); r.status != 200 || len(r.data()) != 0 {
		t.Fatalf("empty prefs: %d %s", r.status, r.raw)
	}
	r := c.do("PATCH", "/api/v1/me/preferences", `{"list.desktop":{"cols":[{"id":"title","w":420},{"id":"status"}]}}`)
	if r.status != 200 || r.data()["list.desktop"] == nil {
		t.Fatalf("patch: %d %s", r.status, r.raw)
	}
	c.do("PATCH", "/api/v1/me/preferences", `{"list.phone":{"cols":[]}}`)
	got := c.do("GET", "/api/v1/me/preferences", nil)
	if len(got.data()) != 2 {
		t.Fatalf("both keys expected: %s", got.raw)
	}
	if r := c.do("PATCH", "/api/v1/me/preferences", `{"Bad Key":1}`); r.status != 422 {
		t.Fatalf("bad key: %d %s", r.status, r.raw)
	}
	tok := c.do("POST", "/api/v1/tokens", map[string]any{"name": "agent"})
	agent := c.withToken(tok.str("data", "secret"))
	if r := agent.do("GET", "/api/v1/me/preferences", nil); r.status != 403 {
		t.Fatalf("token must not read UI preferences: %d", r.status)
	}
}

func TestDeleteProjectOverREST(t *testing.T) {
	h := newHarness(t)
	h.mkUser("alice", true)
	c := h.session("alice")
	c.do("POST", "/api/v1/projects", map[string]any{"name": "Bye", "key": "BYE"})
	c.do("POST", "/api/v1/tasks", map[string]any{"project": "BYE", "title": "t"})
	if r := c.do("DELETE", "/api/v1/projects/BYE?confirm=BYE", nil); r.status != 409 || r.str("code") != "project_not_archived" {
		t.Fatalf("must archive first: %d %s", r.status, r.raw)
	}
	c.do("POST", "/api/v1/projects/BYE/archive", nil)
	if r := c.do("DELETE", "/api/v1/projects/BYE", nil); r.status != 422 {
		t.Fatalf("missing confirm: %d %s", r.status, r.raw)
	}
	tok := c.do("POST", "/api/v1/tokens", map[string]any{"name": "agent"})
	agent := c.withToken(tok.str("data", "secret"))
	if r := agent.do("DELETE", "/api/v1/projects/BYE?confirm=BYE", nil); r.status != 403 {
		t.Fatalf("tokens must not delete projects: %d", r.status)
	}
	if r := c.do("DELETE", "/api/v1/projects/BYE?confirm=BYE", nil); r.status != 204 {
		t.Fatalf("delete: %d %s", r.status, r.raw)
	}
	if r := c.do("GET", "/api/v1/projects/BYE", nil); r.status != 404 {
		t.Fatalf("gone: %d", r.status)
	}
}

func TestTaskTypeOverREST(t *testing.T) {
	h := newHarness(t)
	h.mkUser("alice", true)
	c := h.session("alice")
	c.do("POST", "/api/v1/projects", map[string]any{"name": "Kinds", "key": "KND"})
	meta := c.do("GET", "/api/v1/meta", nil)
	if len(meta.data()["types"].([]any)) != 3 {
		t.Fatalf("meta types: %s", meta.raw)
	}
	a := c.do("POST", "/api/v1/tasks", map[string]any{"project": "KND", "title": "a", "type": "bug"})
	b := c.do("POST", "/api/v1/tasks", map[string]any{"project": "KND", "title": "b"})
	if a.data()["type"] != "bug" || b.data()["type"] != nil {
		t.Fatalf("create: %s / %s", a.raw, b.raw)
	}
	if r := c.do("GET", "/api/v1/tasks?project=KND&type=bug", nil); len(r.list()) != 1 {
		t.Fatalf("filter type=bug: %s", r.raw)
	}
	if r := c.do("GET", "/api/v1/tasks?project=KND&type=none", nil); len(r.list()) != 1 || r.list()[0].(map[string]any)["title"] != "b" {
		t.Fatalf("filter type=none: %s", r.raw)
	}
	if r := c.do("PATCH", "/api/v1/tasks/"+b.str("data", "id"), `{"type":"feature"}`); r.data()["type"] != "feature" {
		t.Fatalf("patch: %s", r.raw)
	}
	if r := c.do("PATCH", "/api/v1/tasks/"+b.str("data", "id"), `{"type":null}`); r.data()["type"] != nil {
		t.Fatalf("null clears: %s", r.raw)
	}
	if r := c.do("PATCH", "/api/v1/tasks/"+b.str("data", "id"), `{"type":"epic"}`); r.status != 422 || !strings.Contains(string(r.raw), "bug") {
		t.Fatalf("invalid type should name valid values: %d %s", r.status, r.raw)
	}
}
