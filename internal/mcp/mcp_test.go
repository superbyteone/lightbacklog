package mcp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/superbyteone/lightbacklog/internal/files"
	"github.com/superbyteone/lightbacklog/internal/service"
	"github.com/superbyteone/lightbacklog/internal/store"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// testAudit builds an AuditSink whose Run loop is stopped and drained on test cleanup.
func testAudit(t *testing.T, svc *service.Service) *AuditSink {
	t.Helper()
	a := NewAuditSink(svc, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { a.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	return a
}

type bearer struct{ token string }

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	if b.token != "" {
		r.Header.Set("Authorization", "Bearer "+b.token)
	}
	return http.DefaultTransport.RoundTrip(r)
}

type fixture struct {
	t     *testing.T
	svc   *service.Service
	srv   *httptest.Server
	alice service.Principal
}

func newFixture(t *testing.T) *fixture { return newFixtureWithCIDRs(t) }

// newFixtureWithCIDRs is newFixture with a non-default LB_MCP_ALLOWED_CIDRS list (as raw
// strings, parsed with ParseAllowedCIDRs); newFixture itself passes none, matching the
// default-safe (local-only) behavior.
func newFixtureWithCIDRs(t *testing.T, cidrs ...string) *fixture {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(context.Background(), filepath.Join(dir, "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	fs, _ := files.NewStore(filepath.Join(dir, "uploads"))
	svc := service.New(db, fs, service.Config{})
	u, err := svc.CreateUser(context.Background(), service.CreateUserInput{Username: "alice", Password: "correct horse battery", IsAdmin: true})
	if err != nil {
		t.Fatal(err)
	}
	allowed, err := ParseAllowedCIDRs(strings.Join(cidrs, ","))
	if err != nil {
		t.Fatal(err)
	}
	// Generous rate limits: functional tests exercise many calls quickly and shouldn't be
	// throttled. TestMCPRateLimit below builds its own low-limit Handler to test throttling.
	srv := httptest.NewServer(Handler(svc, "test", allowed, 1000, 1000, testAudit(t, svc)))
	t.Cleanup(srv.Close)
	return &fixture{t: t, svc: svc, srv: srv, alice: service.Principal{UserID: u.ID, Username: "alice", IsAdmin: true}}
}

func (f *fixture) token(scope string, projects ...string) string {
	f.t.Helper()
	_, secret, err := f.svc.CreateToken(context.Background(), f.alice, service.CreateTokenInput{Name: "t", Scope: scope, Projects: projects})
	if err != nil {
		f.t.Fatal(err)
	}
	return secret
}

func (f *fixture) connect(token string) *sdk.ClientSession {
	f.t.Helper()
	c := sdk.NewClient(&sdk.Implementation{Name: "test-agent", Version: "1"}, nil)
	sess, err := c.Connect(context.Background(), &sdk.StreamableClientTransport{Endpoint: f.srv.URL, HTTPClient: &http.Client{Transport: bearer{token}}, DisableStandaloneSSE: true}, nil)
	if err != nil {
		f.t.Fatalf("connect: %v", err)
	}
	f.t.Cleanup(func() { sess.Close() })
	return sess
}

func call(t *testing.T, s *sdk.ClientSession, tool string, args map[string]any) (map[string]any, *sdk.CallToolResult) {
	t.Helper()
	res, err := s.CallTool(context.Background(), &sdk.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		t.Fatalf("%s: protocol error %v", tool, err)
	}
	var out map[string]any
	if !res.IsError {
		raw, _ := json.Marshal(res.StructuredContent)
		_ = json.Unmarshal(raw, &out)
	}
	return out, res
}

func errText(res *sdk.CallToolResult) string {
	var sb strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*sdk.TextContent); ok {
			sb.WriteString(tc.Text)
		}
	}
	return sb.String()
}

func TestToolsListAndAuth(t *testing.T) {
	f := newFixture(t)
	sess := f.connect(f.token("write"))
	tools, err := sess.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, tl := range tools.Tools {
		names[tl.Name] = true
		if tl.Description == "" {
			t.Errorf("tool %s has no description", tl.Name)
		}
	}
	for _, want := range []string{"list_projects", "get_project", "create_project", "update_project", "archive_project", "list_tasks", "search_tasks",
		"get_task", "create_task", "update_task", "move_task", "delete_task", "get_meta", "list_labels", "create_label", "add_attachment", "list_attachments"} {
		if !names[want] {
			t.Errorf("missing tool %s", want)
		}
	}

	// No token, bad token, and proxied requests are refused before any MCP processing.
	for name, mk := range map[string]func() *http.Request{
		"no token": func() *http.Request { r, _ := http.NewRequest("POST", f.srv.URL, strings.NewReader("{}")); return r },
		"bad token": func() *http.Request {
			r, _ := http.NewRequest("POST", f.srv.URL, strings.NewReader("{}"))
			r.Header.Set("Authorization", "Bearer lb_nope")
			return r
		},
	} {
		resp, _ := http.DefaultClient.Do(mk())
		if resp.StatusCode != 401 {
			t.Errorf("%s: got %d, want 401", name, resp.StatusCode)
		}
	}
	r, _ := http.NewRequest("POST", f.srv.URL, strings.NewReader("{}"))
	r.Header.Set("Authorization", "Bearer "+f.token("write"))
	r.Header.Set("X-Forwarded-For", "203.0.113.9")
	if resp, _ := http.DefaultClient.Do(r); resp.StatusCode != 403 {
		t.Errorf("proxied request got %d, want 403", resp.StatusCode)
	}
}

func TestAgentBacklogWorkflow(t *testing.T) {
	f := newFixture(t)
	s := f.connect(f.token("write"))

	proj, res := call(t, s, "create_project", map[string]any{"name": "Agent Work", "key": "AGW"})
	if res.IsError || proj["key"] != "AGW" {
		t.Fatalf("create_project: %v %s", proj, errText(res))
	}
	meta, _ := call(t, s, "get_meta", nil)
	if len(meta["statuses"].([]any)) != 4 {
		t.Fatalf("meta: %v", meta)
	}

	args := map[string]any{"project": "AGW", "title": "Refactor parser", "priority": "high", "labels": []string{"refactor"}, "external_ref": "PR-7"}
	first, res := call(t, s, "create_task", args)
	if res.IsError || first["created"] != true {
		t.Fatalf("create_task: %s", errText(res))
	}
	second, _ := call(t, s, "create_task", args)
	if second["created"] != false || second["task"].(map[string]any)["id"] != first["task"].(map[string]any)["id"] {
		t.Fatalf("external_ref not idempotent over MCP: %v", second)
	}
	task := first["task"].(map[string]any)

	upd, res := call(t, s, "update_task", map[string]any{"task": task["ref"], "status": "in progress", "description": "- [ ] step one", "add_labels": []string{"urgent"}, "version": task["version"]})
	if res.IsError || upd["status"] != "in_progress" || len(upd["labels"].([]any)) != 2 {
		t.Fatalf("update_task: %v %s", upd, errText(res))
	}
	_, res = call(t, s, "update_task", map[string]any{"task": "AGW-1", "title": "stale", "version": task["version"]})
	if !res.IsError || !strings.Contains(errText(res), "version_conflict") || !strings.Contains(errText(res), "current version") {
		t.Fatalf("stale update should explain the conflict, got %q", errText(res))
	}
	_, res = call(t, s, "update_task", map[string]any{"task": "AGW-1", "status": "wat"})
	if !res.IsError || !strings.Contains(errText(res), "valid values") {
		t.Fatalf("invalid status should list valid values: %q", errText(res))
	}

	found, _ := call(t, s, "search_tasks", map[string]any{"query": "pars"})
	if len(found["tasks"].([]any)) != 1 {
		t.Fatalf("search: %v", found)
	}
	listed, _ := call(t, s, "list_tasks", map[string]any{"status": []string{"in_progress"}, "label": []string{"urgent"}})
	if len(listed["tasks"].([]any)) != 1 || listed["total"].(float64) != 1 {
		t.Fatalf("list_tasks: %v", listed)
	}
	got, _ := call(t, s, "get_task", map[string]any{"task": "AGW-1"})
	if got["description"] != "- [ ] step one" {
		t.Fatalf("get_task: %v", got)
	}

	png := append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 32)...)
	att, res := call(t, s, "add_attachment", map[string]any{"task": "AGW-1", "filename": "s.png", "content_base64": base64.StdEncoding.EncodeToString(png)})
	if res.IsError || !strings.HasPrefix(att["markdown"].(string), "![s.png](/files/") {
		t.Fatalf("add_attachment: %v %s", att, errText(res))
	}
	_, res = call(t, s, "add_attachment", map[string]any{"task": "AGW-1", "filename": "x.svg", "content_base64": base64.StdEncoding.EncodeToString([]byte("<svg/>"))})
	if !res.IsError || !strings.Contains(errText(res), "unsupported_file_type") {
		t.Fatalf("svg should be rejected: %q", errText(res))
	}
	_, res = call(t, s, "add_attachment", map[string]any{"task": "AGW-1", "filename": "x.png", "content_base64": "%%%not base64%%%"})
	if !res.IsError {
		t.Fatal("bad base64 should be an error")
	}
	atts, _ := call(t, s, "list_attachments", map[string]any{"task": "AGW-1"})
	if len(atts["attachments"].([]any)) != 1 {
		t.Fatalf("list_attachments: %v", atts)
	}

	other, _ := call(t, s, "create_project", map[string]any{"name": "Other Place"})
	moved, res := call(t, s, "move_task", map[string]any{"task": "AGW-1", "project": other["key"]})
	if res.IsError || moved["project"].(map[string]any)["key"] != other["key"] {
		t.Fatalf("move_task: %s", errText(res))
	}
	archived, _ := call(t, s, "archive_project", map[string]any{"project": "AGW"})
	if archived["archived"] != true {
		t.Fatalf("archive: %v", archived)
	}
	lbl, _ := call(t, s, "create_label", map[string]any{"name": "Docs"})
	if lbl["name"] != "Docs" {
		t.Fatalf("create_label: %v", lbl)
	}
	del, res := call(t, s, "delete_task", map[string]any{"task": moved["ref"]})
	if res.IsError || del["deleted"] != true {
		t.Fatalf("delete_task: %s", errText(res))
	}
}

func TestReadOnlyAndRestrictedTokensOverMCP(t *testing.T) {
	f := newFixture(t)
	a, _ := f.svc.CreateProject(context.Background(), f.alice, service.CreateProjectInput{Name: "Alpha", Key: "ALP"})
	b, _ := f.svc.CreateProject(context.Background(), f.alice, service.CreateProjectInput{Name: "Beta", Key: "BET"})
	f.svc.CreateTask(context.Background(), f.alice, service.CreateTaskInput{Project: a.ID, Title: "a1"})
	f.svc.CreateTask(context.Background(), f.alice, service.CreateTaskInput{Project: b.ID, Title: "b1"})

	ro := f.connect(f.token("read"))
	if out, _ := call(t, ro, "list_tasks", nil); len(out["tasks"].([]any)) != 2 {
		t.Fatalf("read token should list both: %v", out)
	}
	_, res := call(t, ro, "create_task", map[string]any{"project": "ALP", "title": "nope"})
	if !res.IsError || !strings.Contains(errText(res), "insufficient_scope") {
		t.Fatalf("read-only token wrote: %q", errText(res))
	}

	scoped := f.connect(f.token("write", "ALP"))
	if out, _ := call(t, scoped, "list_tasks", nil); len(out["tasks"].([]any)) != 1 {
		t.Fatalf("scoped token saw other project: %v", out)
	}
	_, res = call(t, scoped, "get_task", map[string]any{"task": "BET-1"})
	if !res.IsError || !strings.Contains(errText(res), "not_found") {
		t.Fatalf("scoped token read out-of-scope task: %q", errText(res))
	}
}

func TestSequenceThroughMCP(t *testing.T) {
	f := newFixture(t)
	s := f.connect(f.token("write"))
	call(t, s, "create_project", map[string]any{"name": "Order", "key": "ORD"})
	first, res := call(t, s, "create_task", map[string]any{"project": "ORD", "title": "later", "sequence": 2})
	if res.IsError || first["task"].(map[string]any)["sequence"].(float64) != 2 {
		t.Fatalf("create with sequence: %s", errText(res))
	}
	call(t, s, "create_task", map[string]any{"project": "ORD", "title": "sooner", "sequence": 1})
	listed, _ := call(t, s, "list_tasks", map[string]any{"project": []string{"ORD"}, "sort": "sequence"})
	if listed["tasks"].([]any)[0].(map[string]any)["title"] != "sooner" {
		t.Fatalf("list sorted by sequence: %v", listed)
	}
	upd, _ := call(t, s, "update_task", map[string]any{"task": "ORD-1", "clear_sequence": true})
	if upd["sequence"] != nil {
		t.Fatalf("clear_sequence: %v", upd)
	}
}

func TestTaskTypeThroughMCP(t *testing.T) {
	f := newFixture(t)
	s := f.connect(f.token("write"))
	call(t, s, "create_project", map[string]any{"name": "Kinds", "key": "KND"})
	meta, _ := call(t, s, "get_meta", nil)
	if len(meta["types"].([]any)) != 3 {
		t.Fatalf("get_meta types: %v", meta)
	}
	created, res := call(t, s, "create_task", map[string]any{"project": "KND", "title": "crash on save", "type": "bug"})
	if res.IsError || created["task"].(map[string]any)["type"] != "bug" {
		t.Fatalf("create with type: %s", errText(res))
	}
	call(t, s, "create_task", map[string]any{"project": "KND", "title": "no type"})
	bugs, _ := call(t, s, "list_tasks", map[string]any{"project": []string{"KND"}, "type": []string{"bug"}})
	if len(bugs["tasks"].([]any)) != 1 {
		t.Fatalf("list by type: %v", bugs)
	}
	upd, _ := call(t, s, "update_task", map[string]any{"task": "KND-1", "type": "feature"})
	if upd["type"] != "feature" {
		t.Fatalf("update type: %v", upd)
	}
	upd, _ = call(t, s, "update_task", map[string]any{"task": "KND-1", "clear_type": true})
	if upd["type"] != nil {
		t.Fatalf("clear_type: %v", upd)
	}
	_, res = call(t, s, "update_task", map[string]any{"task": "KND-1", "type": "epic"})
	if !res.IsError || !strings.Contains(errText(res), "valid values") {
		t.Fatalf("invalid type message: %q", errText(res))
	}
}

func TestMCPAuthFailureThrottle(t *testing.T) {
	f := newFixture(t)
	badReq := func() *http.Request {
		r, _ := http.NewRequest("POST", f.srv.URL, strings.NewReader("{}"))
		r.Header.Set("Authorization", "Bearer lb_nope")
		return r
	}
	var last *http.Response
	for i := 0; i < 11; i++ {
		last, _ = http.DefaultClient.Do(badReq())
	}
	if last.StatusCode != http.StatusTooManyRequests || last.Header.Get("Retry-After") == "" {
		t.Fatalf("expected throttling after repeated bad tokens, got %d", last.StatusCode)
	}
	// A correct token from the same peer is refused too while throttled.
	r, _ := http.NewRequest("POST", f.srv.URL, strings.NewReader("{}"))
	r.Header.Set("Authorization", "Bearer "+f.token("write"))
	if resp, _ := http.DefaultClient.Do(r); resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("throttle bypassed with a valid token: %d", resp.StatusCode)
	}
}

func TestMCPRateLimit(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(context.Background(), filepath.Join(dir, "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	fs, _ := files.NewStore(filepath.Join(dir, "uploads"))
	svc := service.New(db, fs, service.Config{})
	u, err := svc.CreateUser(context.Background(), service.CreateUserInput{Username: "alice", Password: "correct horse battery", IsAdmin: true})
	if err != nil {
		t.Fatal(err)
	}
	alice := service.Principal{UserID: u.ID, Username: "alice", IsAdmin: true}
	_, secret, err := svc.CreateToken(context.Background(), alice, service.CreateTokenInput{Name: "t", Scope: "write"})
	if err != nil {
		t.Fatal(err)
	}

	// burst of 3: the 4th immediate request must be throttled.
	srv := httptest.NewServer(Handler(svc, "test", nil, 1, 3, testAudit(t, svc)))
	t.Cleanup(srv.Close)
	req := func() *http.Request {
		r, _ := http.NewRequest("POST", srv.URL, strings.NewReader("{}"))
		r.Header.Set("Authorization", "Bearer "+secret)
		return r
	}
	var last *http.Response
	for i := 0; i < 4; i++ {
		last, _ = http.DefaultClient.Do(req())
	}
	if last.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("expected the burst to be exhausted, got %d", last.StatusCode)
	}
}

func TestAuditSinkFlushesOnShutdown(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(context.Background(), filepath.Join(dir, "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	fs, _ := files.NewStore(filepath.Join(dir, "uploads"))
	svc := service.New(db, fs, service.Config{})
	u, err := svc.CreateUser(context.Background(), service.CreateUserInput{Username: "alice", Password: "correct horse battery", IsAdmin: true})
	if err != nil {
		t.Fatal(err)
	}
	alice := service.Principal{UserID: u.ID, Username: "alice", IsAdmin: true}
	tok, _, err := svc.CreateToken(context.Background(), alice, service.CreateTokenInput{Name: "t"})
	if err != nil {
		t.Fatal(err)
	}

	a := NewAuditSink(svc, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { a.Run(ctx); close(done) }()

	// Explicit, distinct millisecond timestamps: two same-millisecond calls would otherwise
	// leave "newest first" ordering below undefined.
	now := time.Now()
	a.record(service.MCPAuditEntry{TokenID: tok.ID, UserID: u.ID, Tool: "get_meta", Status: "ok", DurationMS: 3, CreatedAt: now})
	a.record(service.MCPAuditEntry{TokenID: tok.ID, UserID: u.ID, Tool: "create_task", Status: "error", ErrorCode: "validation_failed", DurationMS: 9, CreatedAt: now.Add(time.Millisecond)})

	cancel()
	<-done // Run must drain and flush before returning

	rows, err := svc.ListMCPAudit(context.Background(), tok.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 audit rows, got %d", len(rows))
	}
	// Newest first.
	if rows[0].Tool != "create_task" || rows[0].Status != "error" || rows[0].ErrorCode != "validation_failed" {
		t.Fatalf("unexpected newest row: %+v", rows[0])
	}
	if rows[1].Tool != "get_meta" || rows[1].Status != "ok" || rows[1].ErrorCode != "" {
		t.Fatalf("unexpected oldest row: %+v", rows[1])
	}
}

func TestParseAllowedCIDRs(t *testing.T) {
	if got, err := ParseAllowedCIDRs(""); err != nil || len(got) != 0 {
		t.Fatalf("empty input: %v, %v", got, err)
	}
	got, err := ParseAllowedCIDRs(" 203.0.113.4 , 198.51.100.0/24 ")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].String() != "203.0.113.4/32" || got[1].String() != "198.51.100.0/24" {
		t.Fatalf("unexpected parse result: %+v", got)
	}
	for _, bad := range []string{"not-an-ip", "0.0.0.0/0", "::/0", "203.0.113.4/abc"} {
		if _, err := ParseAllowedCIDRs(bad); err == nil {
			t.Errorf("expected %q to be rejected", bad)
		}
	}
}

func TestMCPAllowedCIDRs(t *testing.T) {
	f := newFixtureWithCIDRs(t, "203.0.113.0/24")
	req := func(xff string) *http.Request {
		r, _ := http.NewRequest("POST", f.srv.URL, strings.NewReader("{}"))
		r.Header.Set("Authorization", "Bearer "+f.token("write"))
		r.Header.Set("X-Real-IP", xff)
		return r
	}
	if resp, _ := http.DefaultClient.Do(req("203.0.113.42")); resp.StatusCode == http.StatusForbidden {
		t.Fatal("in-range peer was rejected")
	}
	if resp, _ := http.DefaultClient.Do(req("198.51.100.7")); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("out-of-range peer should be rejected, got %d", resp.StatusCode)
	}
	// Regression: with an empty allowlist (the default fixture), any proxied request is
	// unconditionally rejected regardless of the IP it claims.
	f2 := newFixture(t)
	r, _ := http.NewRequest("POST", f2.srv.URL, strings.NewReader("{}"))
	r.Header.Set("Authorization", "Bearer "+f2.token("write"))
	r.Header.Set("X-Real-IP", "203.0.113.42")
	if resp, _ := http.DefaultClient.Do(r); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("empty allowlist should reject every proxied request, got %d", resp.StatusCode)
	}
}
