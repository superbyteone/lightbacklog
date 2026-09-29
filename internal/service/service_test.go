package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/superbyteone/lightbacklog/internal/files"
	"github.com/superbyteone/lightbacklog/internal/store"
)

type env struct {
	t     *testing.T
	s     *Service
	ctx   context.Context
	alice Principal
	bob   Principal
}

func newEnv(t *testing.T) *env {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(context.Background(), filepath.Join(dir, "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	fs, err := files.NewStore(filepath.Join(dir, "uploads"))
	if err != nil {
		t.Fatal(err)
	}
	s := New(db, fs, Config{MaxUploadBytes: 1 << 20})
	e := &env{t: t, s: s, ctx: context.Background()}
	e.alice = e.user("alice", true)
	e.bob = e.user("bob", false)
	return e
}

func (e *env) user(name string, admin bool) Principal {
	u, err := e.s.CreateUser(e.ctx, CreateUserInput{Username: name, Password: "correct horse battery", IsAdmin: admin})
	if err != nil {
		e.t.Fatal(err)
	}
	return Principal{UserID: u.ID, Username: u.Username, IsAdmin: admin}
}

func (e *env) project(p Principal, name string) *Project {
	e.t.Helper()
	pr, err := e.s.CreateProject(e.ctx, p, CreateProjectInput{Name: name})
	if err != nil {
		e.t.Fatal(err)
	}
	return pr
}

func (e *env) task(p Principal, project, title string, mod ...func(*CreateTaskInput)) *Task {
	e.t.Helper()
	in := CreateTaskInput{Project: project, Title: title}
	for _, m := range mod {
		m(&in)
	}
	tk, _, err := e.s.CreateTask(e.ctx, p, in)
	if err != nil {
		e.t.Fatal(err)
	}
	return tk
}

func wantCode(t *testing.T, err error, code string) {
	t.Helper()
	var se *Error
	if !errors.As(err, &se) || se.Code != code {
		t.Fatalf("want error code %q, got %v", code, err)
	}
}

func ptr[T any](v T) *T { return &v }

func TestPermissionMatrixAcrossUsers(t *testing.T) {
	e := newEnv(t)
	proj := e.project(e.alice, "Secret Project")
	tk := e.task(e.alice, proj.ID, "Private task")

	// Bob is not a member: every path must look like "not found", never leak existence.
	if _, err := e.s.GetProject(e.ctx, e.bob, proj.ID); err == nil {
		t.Fatal("bob read alice's project")
	} else {
		wantCode(t, err, "not_found")
	}
	if _, err := e.s.GetTask(e.ctx, e.bob, tk.ID); err == nil {
		t.Fatal("bob read alice's task by id")
	}
	if _, err := e.s.GetTask(e.ctx, e.bob, tk.Ref); err == nil {
		t.Fatal("bob read alice's task by ref")
	}
	if _, _, err := e.s.CreateTask(e.ctx, e.bob, CreateTaskInput{Project: proj.ID, Title: "x"}); err == nil {
		t.Fatal("bob created a task in alice's project")
	}
	if _, err := e.s.UpdateTask(e.ctx, e.bob, tk.ID, UpdateTaskInput{Title: ptr("hijack")}); err == nil {
		t.Fatal("bob edited alice's task")
	}
	if err := e.s.DeleteTask(e.ctx, e.bob, tk.ID, nil); err == nil {
		t.Fatal("bob deleted alice's task")
	}
	page, err := e.s.ListTasks(e.ctx, e.bob, TaskFilter{Q: "Private", IncludeTotal: true})
	if err != nil || len(page.Tasks) != 0 || *page.Total != 0 {
		t.Fatalf("bob's search saw alice's tasks: %v %v", page, err)
	}
	page, _ = e.s.ListTasks(e.ctx, e.bob, TaskFilter{Projects: []string{proj.Key}})
	if len(page.Tasks) != 0 {
		t.Fatal("bob listed alice's project by key")
	}
	if list, _ := e.s.ListProjects(e.ctx, e.bob, ProjectFilter{}); len(list) != 0 {
		t.Fatal("bob sees alice's project in listing")
	}
	// Even a site administrator does not automatically gain project access.
	admin := e.user("root", true)
	if _, err := e.s.GetProject(e.ctx, admin, proj.ID); err == nil {
		t.Fatal("admin gained implicit project access")
	}

	// Viewer can read but not write; editor can write but not manage members.
	if err := e.s.SetMember(e.ctx, e.alice, proj.ID, "bob", RoleViewer); err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.GetTask(e.ctx, e.bob, tk.ID); err != nil {
		t.Fatal(err)
	}
	_, err = e.s.UpdateTask(e.ctx, e.bob, tk.ID, UpdateTaskInput{Title: ptr("nope")})
	wantCode(t, err, "forbidden")
	if err := e.s.SetMember(e.ctx, e.alice, proj.ID, "bob", RoleEditor); err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.UpdateTask(e.ctx, e.bob, tk.ID, UpdateTaskInput{Title: ptr("ok")}); err != nil {
		t.Fatal(err)
	}
	wantCode(t, e.s.SetMember(e.ctx, e.bob, proj.ID, "bob", RoleOwner), "forbidden")
	_, err = e.s.UpdateProject(e.ctx, e.bob, proj.ID, UpdateProjectInput{Name: ptr("renamed")})
	wantCode(t, err, "forbidden")
	// The last owner cannot be demoted or removed.
	wantCode(t, e.s.SetMember(e.ctx, e.alice, proj.ID, "alice", RoleEditor), "last_owner")
	wantCode(t, e.s.RemoveMember(e.ctx, e.alice, proj.ID, e.alice.UserID), "last_owner")
}

func TestTokenScopeAndProjectRestriction(t *testing.T) {
	e := newEnv(t)
	a := e.project(e.alice, "Alpha")
	b := e.project(e.alice, "Beta")
	e.task(e.alice, a.ID, "in alpha")
	e.task(e.alice, b.ID, "in beta")

	_, secret, err := e.s.CreateToken(e.ctx, e.alice, CreateTokenInput{Name: "agent", Scope: "write", Projects: []string{a.Key}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(secret, "lb_") {
		t.Fatalf("unexpected secret format %q", secret)
	}
	tp, err := e.s.AuthenticateToken(e.ctx, secret)
	if err != nil {
		t.Fatal(err)
	}
	page, _ := e.s.ListTasks(e.ctx, tp, TaskFilter{})
	if len(page.Tasks) != 1 || page.Tasks[0].Title != "in alpha" {
		t.Fatalf("restricted token saw %d tasks", len(page.Tasks))
	}
	if _, err := e.s.GetProject(e.ctx, tp, b.ID); err == nil {
		t.Fatal("restricted token read other project")
	}
	if _, _, err := e.s.CreateTask(e.ctx, tp, CreateTaskInput{Project: b.ID, Title: "x"}); err == nil {
		t.Fatal("restricted token wrote to other project")
	}
	if _, err := e.s.CreateProject(e.ctx, tp, CreateProjectInput{Name: "New"}); err == nil {
		t.Fatal("restricted token created a project")
	}
	// Tokens can never manage tokens or users.
	if _, _, err := e.s.CreateToken(e.ctx, tp, CreateTokenInput{Name: "child"}); err == nil {
		t.Fatal("token created a token")
	}
	if tp.IsAdmin {
		t.Fatal("token principal must not be admin")
	}

	// A read-only token cannot mutate.
	_, rsecret, _ := e.s.CreateToken(e.ctx, e.alice, CreateTokenInput{Name: "ro", Scope: "read"})
	rp, _ := e.s.AuthenticateToken(e.ctx, rsecret)
	_, _, err = e.s.CreateTask(e.ctx, rp, CreateTaskInput{Project: a.ID, Title: "x"})
	wantCode(t, err, "insufficient_scope")
	if page, _ := e.s.ListTasks(e.ctx, rp, TaskFilter{}); len(page.Tasks) != 2 {
		t.Fatal("read token should see both projects")
	}

	// A token cannot be issued for a project the user cannot access.
	if _, _, err := e.s.CreateToken(e.ctx, e.bob, CreateTokenInput{Name: "x", Projects: []string{a.ID}}); err == nil {
		t.Fatal("bob made a token for alice's project")
	}

	// Revocation is immediate.
	toks, _ := e.s.ListTokens(e.ctx, e.alice)
	for _, tk := range toks {
		if err := e.s.RevokeToken(e.ctx, e.alice, tk.ID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := e.s.AuthenticateToken(e.ctx, secret); err == nil {
		t.Fatal("revoked token still works")
	}
	if _, err := e.s.AuthenticateToken(e.ctx, "lb_garbage"); err == nil {
		t.Fatal("garbage token accepted")
	}
	// Bob cannot revoke Alice's tokens.
	wantCode(t, e.s.RevokeToken(e.ctx, e.bob, toks[0].ID), "not_found")
}

func TestTokenExpiryAndRotation(t *testing.T) {
	e := newEnv(t)
	proj := e.project(e.alice, "Gamma")

	tok, secret, err := e.s.CreateToken(e.ctx, e.alice, CreateTokenInput{Name: "expiring", Scope: "write", Projects: []string{proj.Key}, ExpiresInDays: 1})
	if err != nil {
		t.Fatal(err)
	}
	if tok.ExpiresAt == nil {
		t.Fatal("expected ExpiresAt to be set")
	}
	if _, _, err := e.s.CreateToken(e.ctx, e.alice, CreateTokenInput{Name: "bad", ExpiresInDays: -1}); err == nil {
		t.Fatal("negative expires_in_days accepted")
	}
	if _, err := e.s.AuthenticateToken(e.ctx, secret); err != nil {
		t.Fatalf("not yet expired: %v", err)
	}

	// Past its expiry, the token stops working; other tokens are unaffected.
	e.s.now = func() time.Time { return time.Now().Add(2 * 24 * time.Hour) }
	if _, err := e.s.AuthenticateToken(e.ctx, secret); err == nil {
		t.Fatal("expired token still authenticated")
	}
	e.s.now = time.Now

	// Rotation: the old secret stops working, the new one keeps name/scope/projects/expiry.
	rotated, newSecret, err := e.s.RotateToken(e.ctx, e.alice, tok.ID)
	if err != nil {
		t.Fatal(err)
	}
	if rotated.Name != "expiring" || rotated.Scope != "write" || rotated.ExpiresAt == nil {
		t.Fatalf("rotated token lost its properties: %+v", rotated)
	}
	if _, err := e.s.AuthenticateToken(e.ctx, secret); err == nil {
		t.Fatal("old secret still works after rotation")
	}
	rp, err := e.s.AuthenticateToken(e.ctx, newSecret)
	if err != nil {
		t.Fatalf("rotated secret should authenticate: %v", err)
	}
	if len(rp.Token.ProjectIDs) != 1 || rp.Token.ProjectIDs[0] != proj.ID {
		t.Fatalf("rotated token lost its project restriction: %v", rp.Token.ProjectIDs)
	}

	// Bob cannot rotate Alice's token, and a revoked/unknown id is not_found.
	_, _, err = e.s.RotateToken(e.ctx, e.bob, rotated.ID)
	wantCode(t, err, "not_found")
	if err := e.s.RevokeToken(e.ctx, e.alice, rotated.ID); err != nil {
		t.Fatal(err)
	}
	_, _, err = e.s.RotateToken(e.ctx, e.alice, rotated.ID)
	wantCode(t, err, "not_found")
}

func TestExternalRefIsIdempotentAndVersionsProtectUpdates(t *testing.T) {
	e := newEnv(t)
	proj := e.project(e.alice, "Web App")
	mk := func(title string) (*Task, bool) {
		tk, created, err := e.s.CreateTask(e.ctx, e.alice, CreateTaskInput{Project: proj.Key, Title: title, ExternalRef: ptr("gh-123")})
		if err != nil {
			t.Fatal(err)
		}
		return tk, created
	}
	first, created := mk("First")
	if !created {
		t.Fatal("first create should report created")
	}
	second, created := mk("Retried with different title")
	if created || second.ID != first.ID || second.Title != "First" {
		t.Fatalf("duplicate created or task modified: %+v", second)
	}
	page, _ := e.s.ListTasks(e.ctx, e.alice, TaskFilter{IncludeTotal: true})
	if *page.Total != 1 {
		t.Fatalf("expected 1 task, got %d", *page.Total)
	}

	// Optimistic concurrency.
	u1, err := e.s.UpdateTask(e.ctx, e.alice, first.ID, UpdateTaskInput{Title: ptr("Edited"), Version: ptr(first.Version)})
	if err != nil || u1.Version != first.Version+1 {
		t.Fatalf("update failed: %v %+v", err, u1)
	}
	_, err = e.s.UpdateTask(e.ctx, e.alice, first.ID, UpdateTaskInput{Title: ptr("Stale"), Version: ptr(first.Version)})
	wantCode(t, err, "version_conflict")
	var se *Error
	if errors.As(err, &se); se.Current == nil {
		t.Fatal("conflict should carry the current task")
	}
	// A no-op update does not bump the version.
	u2, _ := e.s.UpdateTask(e.ctx, e.alice, first.ID, UpdateTaskInput{Title: ptr("Edited")})
	if u2.Version != u1.Version {
		t.Fatal("no-op update bumped the version")
	}
	// Clearing the external ref via explicit null.
	var in UpdateTaskInput
	in.ExternalRef.Set = true
	u3, err := e.s.UpdateTask(e.ctx, e.alice, first.ID, in)
	if err != nil || u3.ExternalRef != nil {
		t.Fatalf("clearing external_ref failed: %v %+v", err, u3)
	}
	wantCode(t, e.s.DeleteTask(e.ctx, e.alice, first.ID, ptr(int64(999))), "version_conflict")
	if err := e.s.DeleteTask(e.ctx, e.alice, first.Ref, nil); err != nil {
		t.Fatal(err)
	}
	_, err = e.s.GetTask(e.ctx, e.alice, first.ID)
	wantCode(t, err, "not_found")
}

func TestTaskRefsMovesLabelsAndValidation(t *testing.T) {
	e := newEnv(t)
	web := e.project(e.alice, "Web App") // key WA
	api := e.project(e.alice, "API")     // key API
	if web.Key != "WA" || api.Key != "API" {
		t.Fatalf("derived keys %q %q", web.Key, api.Key)
	}
	dup := e.project(e.alice, "Wide Alley") // key WA taken -> WA2
	if dup.Key != "WA2" {
		t.Fatalf("expected suffixed key, got %q", dup.Key)
	}
	t1 := e.task(e.alice, web.ID, "one", func(in *CreateTaskInput) {
		in.Labels = []string{"bug", "Backend"}
		in.Priority = "High"
		in.Status = "in progress"
	})
	t2 := e.task(e.alice, web.ID, "two")
	if t1.Ref != "WA-1" || t2.Ref != "WA-2" || t1.Status != "in_progress" || t1.Priority != "high" || len(t1.Labels) != 2 {
		t.Fatalf("unexpected tasks %+v %+v", t1, t2)
	}
	if t2.Priority != "low" {
		t.Fatalf("a task created without a priority should default to low, got %q", t2.Priority)
	}
	got, err := e.s.GetTask(e.ctx, e.alice, "wa-2") // refs are case-insensitive on key
	if err != nil || got.ID != t2.ID {
		t.Fatalf("ref lookup failed: %v", err)
	}
	// Move to another project: new number, same id.
	moved, err := e.s.UpdateTask(e.ctx, e.alice, t1.ID, UpdateTaskInput{Project: ptr("API")})
	if err != nil || moved.Ref != "API-1" || moved.ID != t1.ID || moved.Project.Key != "API" {
		t.Fatalf("move failed: %v %+v", err, moved)
	}
	// Label operations.
	upd, err := e.s.UpdateTask(e.ctx, e.alice, t1.ID, UpdateTaskInput{AddLabels: []string{"urgent"}, RemoveLabels: []string{"BUG"}})
	if err != nil || len(upd.Labels) != 2 {
		t.Fatalf("label ops: %v %+v", err, upd.Labels)
	}
	_, err = e.s.UpdateTask(e.ctx, e.alice, t1.ID, UpdateTaskInput{Labels: &[]string{"never-seen"}, CreateMissingLabels: ptr(false)})
	wantCode(t, err, "validation_failed")

	// Validation errors are field-level and machine readable.
	_, _, err = e.s.CreateTask(e.ctx, e.alice, CreateTaskInput{Project: web.ID, Title: "  ", Status: "wat", DueDate: ptr("31/12/2026")})
	wantCode(t, err, "validation_failed")
	_, _, err = e.s.CreateTask(e.ctx, e.alice, CreateTaskInput{Project: web.ID, Title: "x", Status: "wat"})
	wantCode(t, err, "validation_failed")
	if !strings.Contains(err.Error(), "todo") {
		t.Fatalf("error should list valid statuses: %v", err)
	}
	_, _, err = e.s.CreateTask(e.ctx, e.alice, CreateTaskInput{Project: web.ID, Title: "x", DueDate: ptr("2026-02-30")})
	wantCode(t, err, "validation_failed")

	// Archived projects are read-only for tasks but keep them.
	if _, err := e.s.SetProjectArchived(e.ctx, e.alice, web.ID, true); err != nil {
		t.Fatal(err)
	}
	_, _, err = e.s.CreateTask(e.ctx, e.alice, CreateTaskInput{Project: web.ID, Title: "late"})
	wantCode(t, err, "project_archived")
	if list, _ := e.s.ListProjects(e.ctx, e.alice, ProjectFilter{}); len(list) != 2 {
		t.Fatalf("active listing should hide archived project, got %d", len(list))
	}
	if list, _ := e.s.ListProjects(e.ctx, e.alice, ProjectFilter{Archived: "archived"}); len(list) != 1 {
		t.Fatal("archived listing wrong")
	}
	page, _ := e.s.ListTasks(e.ctx, e.alice, TaskFilter{})
	for _, tk := range page.Tasks {
		if tk.Project.ID == web.ID {
			t.Fatal("tasks of archived projects must be hidden by default")
		}
	}
	page, _ = e.s.ListTasks(e.ctx, e.alice, TaskFilter{Projects: []string{web.Key}})
	if len(page.Tasks) != 1 {
		t.Fatal("explicit project filter should show archived project's tasks")
	}
}

func TestListFiltersSortingAndPagination(t *testing.T) {
	e := newEnv(t)
	proj := e.project(e.alice, "Big")
	other := e.project(e.alice, "Other")
	const n = 25
	for i := 0; i < n; i++ {
		pri := []string{"high", "medium", "low"}[i%3]
		st := []string{"todo", "in_progress", "blocked", "done"}[i%4]
		title := fmt.Sprintf("Task %02d", i)
		e.task(e.alice, proj.ID, title, func(in *CreateTaskInput) {
			in.Priority, in.Status = pri, st
			if i%2 == 0 {
				in.Labels = []string{"even"}
			}
			if i%5 == 0 {
				d := fmt.Sprintf("2026-10-%02d", i+1)
				in.DueDate = &d
			}
		})
	}
	e.task(e.alice, other.ID, "Elsewhere")

	for _, sortBy := range []string{"title", "-title", "priority", "-updated_at", "due_date", "-due_date", "status", "project", "created_at", "completed_at", "-completed_at", "position"} {
		seen, cursor := map[string]bool{}, ""
		var pages int
		for {
			page, err := e.s.ListTasks(e.ctx, e.alice, TaskFilter{Sort: sortBy, Limit: 7, Cursor: cursor})
			if err != nil {
				t.Fatalf("sort %s: %v", sortBy, err)
			}
			for _, tk := range page.Tasks {
				if seen[tk.ID] {
					t.Fatalf("sort %s: duplicate %s across pages", sortBy, tk.ID)
				}
				seen[tk.ID] = true
			}
			pages++
			if page.NextCursor == "" {
				break
			}
			cursor = page.NextCursor
		}
		if len(seen) != n+1 {
			t.Fatalf("sort %s: paged through %d tasks, want %d", sortBy, len(seen), n+1)
		}
	}
	// Sorted order is actually sorted.
	page, _ := e.s.ListTasks(e.ctx, e.alice, TaskFilter{Sort: "-title", Limit: 5})
	if page.Tasks[0].Title != "Task 24" && page.Tasks[0].Title != "Elsewhere" {
		t.Fatalf("unexpected first task %q", page.Tasks[0].Title)
	}
	// A cursor from one sort order is rejected under another.
	_, err := e.s.ListTasks(e.ctx, e.alice, TaskFilter{Sort: "title", Limit: 5, Cursor: page.NextCursor})
	wantCode(t, err, "validation_failed")

	count := func(f TaskFilter) int {
		f.IncludeTotal = true
		f.Limit = 1
		pg, err := e.s.ListTasks(e.ctx, e.alice, f)
		if err != nil {
			t.Fatal(err)
		}
		return *pg.Total
	}
	if got := count(TaskFilter{Projects: []string{proj.Key}, Priorities: []string{"High"}}); got != 9 {
		t.Fatalf("high-priority count %d, want 9", got)
	}
	if got := count(TaskFilter{Statuses: []string{"done", "blocked"}}); got != 12 {
		t.Fatalf("done|blocked count %d, want 12", got)
	}
	if got := count(TaskFilter{Labels: []string{"EVEN"}}); got != 13 {
		t.Fatalf("label count %d, want 13", got)
	}
	if got := count(TaskFilter{DueBefore: "2026-10-11", HasDue: ptr(true)}); got != 3 {
		t.Fatalf("due count %d, want 3", got)
	}
	if got := count(TaskFilter{HasDue: ptr(false)}); got != 26-5 {
		t.Fatalf("no-due count %d", got)
	}
}

func TestSearchByTextPrefixAndRef(t *testing.T) {
	e := newEnv(t)
	proj := e.project(e.alice, "Search")
	e.task(e.alice, proj.ID, "Fix login redirect loop", func(in *CreateTaskInput) { in.Description = "Users get stuck after OAuth callback" })
	e.task(e.alice, proj.ID, "Write onboarding docs")
	find := func(q string) int {
		pg, err := e.s.ListTasks(e.ctx, e.alice, TaskFilter{Q: q})
		if err != nil {
			t.Fatal(err)
		}
		return len(pg.Tasks)
	}
	if find("redir") != 1 || find("oauth callback") != 1 || find("login docs") != 0 || find("onboarding") != 1 {
		t.Fatal("text search results wrong")
	}
	if find(`"; DROP TABLE tasks; --`) != 0 || find("***") != 0 {
		t.Fatal("hostile queries should simply match nothing")
	}
	if find("SEAR-1") != 1 { // key derived from a single word: first four letters
		t.Fatalf("ref search failed")
	}
	pg, err := e.s.ListTasks(e.ctx, e.alice, TaskFilter{Q: "login", Sort: "relevance"})
	if err != nil || len(pg.Tasks) != 1 {
		t.Fatalf("relevance sort: %v", err)
	}
}

func TestUploadValidationAndDescriptionLinking(t *testing.T) {
	e := newEnv(t)
	proj := e.project(e.alice, "Files")
	tk := e.task(e.alice, proj.ID, "with image")
	png := append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{0}, 64)...)

	a, err := e.s.Upload(e.ctx, e.alice, "", tk.ID, "shot.html", bytes.NewReader(png)) // name lies, bytes decide
	if err != nil || a.MIME != "image/png" || !a.Inline {
		t.Fatalf("png upload: %v %+v", err, a)
	}
	for name, body := range map[string]string{
		"svg":    `<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`,
		"xmlsvg": `<?xml version="1.0"?><svg xmlns="http://www.w3.org/2000/svg"/>`,
		"html":   `<!DOCTYPE html><html><script>alert(1)</script></html>`,
		"binary": "\x7fELF\x02\x01\x01\x00\x00\x00\x00\x00\x00\x00\x00\x00",
	} {
		_, err := e.s.Upload(e.ctx, e.alice, "", tk.ID, name, strings.NewReader(body))
		wantCode(t, err, "unsupported_file_type")
	}
	if _, err := e.s.Upload(e.ctx, e.alice, "", tk.ID, "notes.txt", strings.NewReader("plain text log\nline 2")); err != nil {
		t.Fatal(err)
	}
	_, err = e.s.Upload(e.ctx, e.alice, "", tk.ID, "big.txt", bytes.NewReader(bytes.Repeat([]byte("a"), (1<<20)+1)))
	wantCode(t, err, "file_too_large")
	_, err = e.s.Upload(e.ctx, e.alice, "", tk.ID, "empty.txt", strings.NewReader(""))
	wantCode(t, err, "validation_failed")

	// Access control on reads.
	if _, f, err := e.s.OpenAttachment(e.ctx, e.alice, a.ID); err != nil {
		t.Fatal(err)
	} else {
		f.Close()
	}
	if _, _, err := e.s.OpenAttachment(e.ctx, e.bob, a.ID); err == nil {
		t.Fatal("bob opened alice's file")
	}
	if _, err := e.s.Upload(e.ctx, e.bob, "", tk.ID, "x.txt", strings.NewReader("x")); err == nil {
		t.Fatal("bob uploaded to alice's task")
	}
	if _, err := e.s.ListAttachments(e.ctx, e.bob, tk.ID); err == nil {
		t.Fatal("bob listed alice's attachments")
	}

	// A pasted image uploaded before the task exists is linked when the description references it.
	pre, err := e.s.Upload(e.ctx, e.alice, proj.Key, "", "paste.png", bytes.NewReader(png))
	if err != nil || pre.TaskID != nil {
		t.Fatalf("pre-upload: %v", err)
	}
	nt := e.task(e.alice, proj.ID, "has screenshot", func(in *CreateTaskInput) { in.Description = "see ![x](" + pre.URL + ")" })
	atts, _ := e.s.ListAttachments(e.ctx, e.alice, nt.ID)
	if len(atts) != 1 || atts[0].ID != pre.ID {
		t.Fatalf("upload not linked: %+v", atts)
	}
	// Deleting the task garbage-collects its blob.
	if err := e.s.DeleteTask(e.ctx, e.alice, nt.ID, nil); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.s.OpenAttachment(e.ctx, e.alice, pre.ID); err == nil {
		t.Fatal("attachment survived task deletion")
	}
}

func TestSessionsAndPasswords(t *testing.T) {
	e := newEnv(t)
	sess, err := e.s.Login(e.ctx, "ALICE", "correct horse battery", "test")
	if err != nil {
		t.Fatal(err)
	}
	p, err := e.s.AuthenticateSession(e.ctx, sess.Secret)
	if err != nil || p.UserID != e.alice.UserID || !p.IsAdmin {
		t.Fatalf("session auth: %v", err)
	}
	_, err = e.s.Login(e.ctx, "alice", "wrong password!", "test")
	wantCode(t, err, "invalid_credentials")
	_, err = e.s.Login(e.ctx, "nobody", "whatever password", "test")
	wantCode(t, err, "invalid_credentials")
	if _, err := e.s.AuthenticateSession(e.ctx, "not-a-session"); err == nil {
		t.Fatal("bogus session accepted")
	}

	// Changing the password revokes other sessions but keeps the current one.
	other, _ := e.s.Login(e.ctx, "alice", "correct horse battery", "other")
	if err := e.s.ChangePassword(e.ctx, p, "correct horse battery", "a brand new passphrase", sess.Secret); err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.AuthenticateSession(e.ctx, other.Secret); err == nil {
		t.Fatal("other session survived password change")
	}
	if _, err := e.s.AuthenticateSession(e.ctx, sess.Secret); err != nil {
		t.Fatal("current session should survive")
	}
	if _, err := e.s.Login(e.ctx, "alice", "a brand new passphrase", "x"); err != nil {
		t.Fatal(err)
	}
	wantCode(t, e.s.ChangePassword(e.ctx, p, "wrong", "another long passphrase", sess.Secret), "validation_failed")

	// Expiry: absolute and idle.
	e.s.now = func() time.Time { return time.Now().Add(8 * 24 * time.Hour) }
	if _, err := e.s.AuthenticateSession(e.ctx, sess.Secret); err == nil {
		t.Fatal("idle session should have expired")
	}
	e.s.now = time.Now

	// Disabling a user kills their sessions and blocks login.
	s2, _ := e.s.Login(e.ctx, "bob", "correct horse battery", "x")
	if err := e.s.SetUserDisabled(e.ctx, p, e.bob.UserID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.AuthenticateSession(e.ctx, s2.Secret); err == nil {
		t.Fatal("disabled user's session still valid")
	}
	if _, err := e.s.Login(e.ctx, "bob", "correct horse battery", "x"); err == nil {
		t.Fatal("disabled user logged in")
	}
	// Non-admins cannot manage users.
	if _, err := e.s.ListUsers(e.ctx, e.bob); err == nil {
		t.Fatal("non-admin listed users")
	}
	wantCode(t, func() error {
		_, err := e.s.CreateUser(e.ctx, CreateUserInput{Username: "x y", Password: "long enough pw"})
		return err
	}(), "validation_failed")
	wantCode(t, func() error {
		_, err := e.s.CreateUser(e.ctx, CreateUserInput{Username: "carol", Password: "short"})
		return err
	}(), "validation_failed")
	wantCode(t, func() error {
		_, err := e.s.CreateUser(e.ctx, CreateUserInput{Username: "Alice", Password: "long enough pw"})
		return err
	}(), "username_taken")
}

// TestConcurrentLoginsStayWithinMemoryBudget guards against an OOM kill in the 128 MB container:
// argon2id is memory-hard, so unbounded parallel logins once exhausted it.
func TestConcurrentLoginsStayWithinMemoryBudget(t *testing.T) {
	e := newEnv(t)
	debug.FreeOSMemory()
	var wg sync.WaitGroup
	errs := make(chan error, 24)
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := e.s.Login(e.ctx, "alice", "correct horse battery", "load"); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	if used := m.HeapSys + m.StackSys; used > 96<<20 {
		t.Fatalf("24 concurrent logins reserved %d MiB; must stay well under the 128 MiB container limit", used>>20)
	}
}

func recv(t *testing.T, sub *Subscription, within time.Duration) (Event, bool) {
	t.Helper()
	select {
	case ev := <-sub.C:
		return ev, true
	case <-time.After(within):
		return Event{}, false
	}
}

func TestEventsAreScopedToProjectMembers(t *testing.T) {
	e := newEnv(t)
	proj := e.project(e.alice, "Live")
	aliceSub, stopA := e.s.Subscribe(e.alice)
	defer stopA()
	bobSub, stopB := e.s.Subscribe(e.bob)
	defer stopB()

	ctx := WithOrigin(e.ctx, "tab-1")
	tk, _, err := e.s.CreateTask(ctx, e.alice, CreateTaskInput{Project: proj.ID, Title: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	ev, ok := recv(t, aliceSub, time.Second)
	if !ok || ev.Type != "task.created" || ev.TaskID != tk.ID || ev.Origin != "tab-1" || ev.Ref != tk.Ref {
		t.Fatalf("alice should get task.created with origin, got %+v ok=%v", ev, ok)
	}
	if ev, ok := recv(t, bobSub, 150*time.Millisecond); ok {
		t.Fatalf("bob is not a member and must receive nothing, got %+v", ev)
	}

	drain := func(sub *Subscription) (types []string) {
		for {
			ev, ok := recv(t, sub, 150*time.Millisecond)
			if !ok {
				return
			}
			types = append(types, ev.Type)
		}
	}
	has := func(types []string, want string) bool {
		for _, ty := range types {
			if ty == want {
				return true
			}
		}
		return false
	}

	// Adding bob resyncs him and from then on he sees changes.
	if err := e.s.SetMember(e.ctx, e.alice, proj.ID, "bob", RoleViewer); err != nil {
		t.Fatal(err)
	}
	if got := drain(bobSub); !has(got, "resync") {
		t.Fatalf("bob should be told to resync after being added, got %v", got)
	}
	e.task(e.alice, proj.ID, "second")
	if got := drain(bobSub); len(got) == 0 || got[len(got)-1] != "task.created" {
		t.Fatalf("bob (viewer) should see task.created, got %v", got)
	}

	// No-op updates publish nothing; real ones publish task.updated; deletes publish task.deleted.
	drain(aliceSub)
	e.s.UpdateTask(e.ctx, e.alice, tk.ID, UpdateTaskInput{Title: ptr("hello")})
	if ev, ok := recv(t, aliceSub, 150*time.Millisecond); ok {
		t.Fatalf("no-op update must not publish, got %+v", ev)
	}
	e.s.UpdateTask(e.ctx, e.alice, tk.ID, UpdateTaskInput{Title: ptr("changed")})
	if ev, ok := recv(t, aliceSub, time.Second); !ok || ev.Type != "task.updated" {
		t.Fatalf("update should publish, got %+v", ev)
	}
	e.s.DeleteTask(e.ctx, e.alice, tk.ID, nil)
	if ev, ok := recv(t, aliceSub, time.Second); !ok || ev.Type != "task.deleted" || ev.TaskID != tk.ID {
		t.Fatalf("delete should publish, got %+v", ev)
	}

	// Removing bob resyncs him (so his UI drops the project) and silences further events.
	drain(bobSub)
	if err := e.s.RemoveMember(e.ctx, e.alice, proj.ID, e.bob.UserID); err != nil {
		t.Fatal(err)
	}
	if got := drain(bobSub); !has(got, "resync") {
		t.Fatalf("removed member should get resync, got %v", got)
	}
	e.task(e.alice, proj.ID, "third")
	if got := drain(bobSub); len(got) != 0 {
		t.Fatalf("removed member still receives events: %v", got)
	}

	// A token restricted to another project never receives this project's events.
	other := e.project(e.alice, "Elsewhere")
	_, secret, _ := e.s.CreateToken(e.ctx, e.alice, CreateTokenInput{Name: "t", Projects: []string{other.Key}})
	tp, _ := e.s.AuthenticateToken(e.ctx, secret)
	tsub, stopT := e.s.Subscribe(tp)
	defer stopT()
	e.task(e.alice, proj.ID, "fourth")
	if ev, ok := recv(t, tsub, 150*time.Millisecond); ok {
		t.Fatalf("restricted token got an event for a project outside its scope: %+v", ev)
	}
	e.task(e.alice, other.ID, "in scope")
	if ev, ok := recv(t, tsub, time.Second); !ok || ev.Type != "task.created" {
		t.Fatalf("token should get events for its own project, got %+v", ev)
	}
}

func TestSlowSubscriberGetsResyncInsteadOfBlockingWriters(t *testing.T) {
	e := newEnv(t)
	proj := e.project(e.alice, "Flood")
	sub, stop := e.s.Subscribe(e.alice)
	defer stop()
	done := make(chan struct{})
	go func() {
		for i := 0; i < 80; i++ { // far more than the 32-event buffer, and nobody is reading
			e.task(e.alice, proj.ID, fmt.Sprintf("t%d", i))
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("writers blocked by a slow subscriber")
	}
	sawResync := false
	for {
		ev, ok := recv(t, sub, 100*time.Millisecond)
		if !ok {
			break
		}
		if ev.Type == "resync" {
			sawResync = true
		}
	}
	if !sawResync {
		t.Fatal("overflowed subscriber should have been told to resync")
	}
}

func TestTaskSequenceOrdersWorkAndCanBeCleared(t *testing.T) {
	e := newEnv(t)
	proj := e.project(e.alice, "Sequenced")
	mk := func(title string, seq *int) *Task {
		return e.task(e.alice, proj.ID, title, func(in *CreateTaskInput) { in.Sequence = seq })
	}
	a, b, c, none1, none2 := mk("third", ptr(3)), mk("first", ptr(1)), mk("second", ptr(2)), mk("unsequenced one", nil), mk("unsequenced two", nil)
	if a.Sequence == nil || *a.Sequence != 3 || none1.Sequence != nil {
		t.Fatalf("sequence not stored/omitted: %v %v", a.Sequence, none1.Sequence)
	}
	titles := func(sort string) []string {
		var out []string
		cursor := ""
		for {
			pg, err := e.s.ListTasks(e.ctx, e.alice, TaskFilter{Sort: sort, Limit: 2, Cursor: cursor})
			if err != nil {
				t.Fatal(err)
			}
			for _, tk := range pg.Tasks {
				out = append(out, tk.Title)
			}
			if pg.NextCursor == "" {
				return out
			}
			cursor = pg.NextCursor
		}
	}
	got := titles("sequence")
	if len(got) != 5 || got[0] != "first" || got[1] != "second" || got[2] != "third" {
		t.Fatalf("ascending by sequence should start 1,2,3 and paginate cleanly, got %v", got)
	}
	if desc := titles("-sequence"); desc[len(desc)-1] != "first" {
		t.Fatalf("descending should end with sequence 1, got %v", desc)
	}
	// Change, no-op, and clear.
	u, err := e.s.UpdateTask(e.ctx, e.alice, none1.ID, UpdateTaskInput{Sequence: Opt[int]{Set: true, Value: ptr(1)}})
	if err != nil || u.Sequence == nil || *u.Sequence != 1 {
		t.Fatalf("set sequence: %v %+v", err, u)
	}
	same, _ := e.s.UpdateTask(e.ctx, e.alice, none1.ID, UpdateTaskInput{Sequence: Opt[int]{Set: true, Value: ptr(1)}})
	if same.Version != u.Version {
		t.Fatal("setting the same sequence must not bump the version")
	}
	cleared, err := e.s.UpdateTask(e.ctx, e.alice, none1.ID, UpdateTaskInput{Sequence: Opt[int]{Set: true}})
	if err != nil || cleared.Sequence != nil || cleared.Version != u.Version+1 {
		t.Fatalf("clear sequence: %v %+v", err, cleared)
	}
	// Validation.
	for _, bad := range []int{-1, 1_000_001} {
		_, err := e.s.UpdateTask(e.ctx, e.alice, b.ID, UpdateTaskInput{Sequence: Opt[int]{Set: true, Value: ptr(bad)}})
		wantCode(t, err, "validation_failed")
		_, _, err = e.s.CreateTask(e.ctx, e.alice, CreateTaskInput{Project: proj.ID, Title: "x", Sequence: ptr(bad)})
		wantCode(t, err, "validation_failed")
	}
	_ = c
	_ = none2
	// Moving keeps the sequence.
	other := e.project(e.alice, "Elsewhere2")
	m, err := e.s.UpdateTask(e.ctx, e.alice, b.ID, UpdateTaskInput{Project: ptr(other.Key)})
	if err != nil || m.Sequence == nil || *m.Sequence != 1 {
		t.Fatalf("move should keep sequence: %v %+v", err, m)
	}
}

func TestPreferencesMergeIsolationAndLimits(t *testing.T) {
	e := newEnv(t)
	raw := func(s string) json.RawMessage { return json.RawMessage(s) }
	got, err := e.s.GetPreferences(e.ctx, e.alice)
	if err != nil || len(got) != 0 {
		t.Fatalf("new user should have empty preferences: %v %v", got, err)
	}
	if _, err := e.s.MergePreferences(e.ctx, e.alice, map[string]json.RawMessage{"list.desktop": raw(`{"cols":[{"id":"title","w":400}]}`)}); err != nil {
		t.Fatal(err)
	}
	// A second device saving a different key must not overwrite the first.
	out, err := e.s.MergePreferences(e.ctx, e.alice, map[string]json.RawMessage{"list.phone": raw(`{"cols":[]}`)})
	if err != nil || len(out) != 2 {
		t.Fatalf("merge should keep both keys: %v %v", out, err)
	}
	out, _ = e.s.MergePreferences(e.ctx, e.alice, map[string]json.RawMessage{"list.phone": raw(`null`)})
	if len(out) != 1 || out["list.desktop"] == nil {
		t.Fatalf("null should delete only that key: %v", out)
	}
	// Preferences are private per user.
	if bob, _ := e.s.GetPreferences(e.ctx, e.bob); len(bob) != 0 {
		t.Fatalf("bob sees alice's preferences: %v", bob)
	}
	// Validation.
	for name, patch := range map[string]map[string]json.RawMessage{
		"bad key":      {"Bad Key!": raw(`1`)},
		"invalid json": {"ok": json.RawMessage(`{nope`)},
		"too large":    {"big": raw(`"` + strings.Repeat("x", 17<<10) + `"`)},
	} {
		if _, err := e.s.MergePreferences(e.ctx, e.alice, patch); err == nil {
			t.Fatalf("%s should be rejected", name)
		}
	}
	many := map[string]json.RawMessage{}
	for i := 0; i < 40; i++ {
		many[fmt.Sprintf("k%d", i)] = raw(`1`)
	}
	if _, err := e.s.MergePreferences(e.ctx, e.alice, many); err == nil {
		t.Fatal("more than 32 keys should be rejected")
	}
	// Tokens (agents) cannot touch UI preferences.
	_, secret, _ := e.s.CreateToken(e.ctx, e.alice, CreateTokenInput{Name: "t"})
	tp, _ := e.s.AuthenticateToken(e.ctx, secret)
	if _, err := e.s.GetPreferences(e.ctx, tp); err == nil {
		t.Fatal("token read preferences")
	}
}

func TestDeleteProjectIsGuardedAndCleansUp(t *testing.T) {
	e := newEnv(t)
	proj := e.project(e.alice, "Doomed")
	keep := e.project(e.alice, "Survivor")
	tk := e.task(e.alice, proj.ID, "will go", func(in *CreateTaskInput) { in.Labels = []string{"shared-label"} })
	e.task(e.alice, keep.ID, "stays", func(in *CreateTaskInput) { in.Labels = []string{"shared-label"} })
	png := append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{9}, 64)...)
	att, err := e.s.Upload(e.ctx, e.alice, "", tk.ID, "x.png", bytes.NewReader(png))
	if err != nil {
		t.Fatal(err)
	}
	if err := e.s.SetMember(e.ctx, e.alice, proj.ID, "bob", RoleEditor); err != nil {
		t.Fatal(err)
	}
	// Tokens: one limited to only the doomed project, one limited to both.
	_, onlySecret, _ := e.s.CreateToken(e.ctx, e.alice, CreateTokenInput{Name: "only", Projects: []string{proj.Key}})
	_, bothSecret, _ := e.s.CreateToken(e.ctx, e.alice, CreateTokenInput{Name: "both", Projects: []string{proj.Key, keep.Key}})

	// Guards, in order.
	wantCode(t, e.s.DeleteProject(e.ctx, e.alice, proj.ID, proj.Key), "project_not_archived")
	if _, err := e.s.SetProjectArchived(e.ctx, e.alice, proj.ID, true); err != nil {
		t.Fatal(err)
	}
	wantCode(t, e.s.DeleteProject(e.ctx, e.alice, proj.ID, "WRONG"), "validation_failed")
	wantCode(t, e.s.DeleteProject(e.ctx, e.bob, proj.ID, proj.Key), "forbidden") // an editor is not an owner
	tp, _ := e.s.AuthenticateToken(e.ctx, bothSecret)
	if err := e.s.DeleteProject(e.ctx, tp, proj.ID, proj.Key); err == nil {
		t.Fatal("an API token must not be able to delete projects")
	}
	stranger := e.user("carol", false)
	wantCode(t, e.s.DeleteProject(e.ctx, stranger, proj.ID, proj.Key), "not_found")

	sub, stop := e.s.Subscribe(e.bob)
	defer stop()
	if err := e.s.DeleteProject(e.ctx, e.alice, proj.Key, strings.ToLower(proj.Key)); err != nil { // confirm is case-insensitive
		t.Fatal(err)
	}
	// Gone: project, task, file, membership; the other project and the shared label survive.
	if _, err := e.s.GetProject(e.ctx, e.alice, proj.ID); err == nil {
		t.Fatal("project still exists")
	}
	if _, err := e.s.GetTask(e.ctx, e.alice, tk.ID); err == nil {
		t.Fatal("task survived its project")
	}
	if _, _, err := e.s.OpenAttachment(e.ctx, e.alice, att.ID); err == nil {
		t.Fatal("attachment survived its project")
	}
	if list, _ := e.s.ListProjects(e.ctx, e.bob, ProjectFilter{Archived: "all"}); len(list) != 0 {
		t.Fatal("bob still sees the deleted project")
	}
	if pg, _ := e.s.ListTasks(e.ctx, e.alice, TaskFilter{}); len(pg.Tasks) != 1 || pg.Tasks[0].Title != "stays" {
		t.Fatalf("other projects must be untouched: %+v", pg.Tasks)
	}
	if labels, _ := e.s.ListLabels(e.ctx, e.alice); len(labels) != 1 {
		t.Fatalf("labels are shared and must survive, got %d", len(labels))
	}
	if got := drain2(sub); !contains(got, "resync") {
		t.Fatalf("members should be told to resync, got %v", got)
	}
	// Token safety: the token that was limited to ONLY this project must not turn into an unrestricted one.
	if _, err := e.s.AuthenticateToken(e.ctx, onlySecret); err == nil {
		t.Fatal("a token restricted to only the deleted project must be revoked, not widened to all projects")
	}
	tp2, err := e.s.AuthenticateToken(e.ctx, bothSecret)
	if err != nil {
		t.Fatalf("a token that also covers another project keeps working: %v", err)
	}
	if pg, _ := e.s.ListTasks(e.ctx, tp2, TaskFilter{}); len(pg.Tasks) != 1 {
		t.Fatal("the surviving token must still be limited to its remaining project")
	}
	// The key is free again.
	if _, err := e.s.CreateProject(e.ctx, e.alice, CreateProjectInput{Name: "Doomed", Key: proj.Key}); err != nil {
		t.Fatalf("the key should be reusable: %v", err)
	}
}

func TestProjectFavoriteIsPerUser(t *testing.T) {
	e := newEnv(t)
	proj := e.project(e.alice, "Shared")
	if err := e.s.SetMember(e.ctx, e.alice, proj.ID, "bob", RoleViewer); err != nil {
		t.Fatal(err)
	}

	// Fresh projects are not favorited.
	if got, _ := e.s.GetProject(e.ctx, e.alice, proj.ID); got.Favorite {
		t.Fatal("a new project should not start favorited")
	}

	// Alice favorites it; a viewer (not just an owner) may do this for themselves.
	pr, err := e.s.SetProjectFavorite(e.ctx, e.bob, proj.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if !pr.Favorite {
		t.Fatal("bob's favorite should be reflected in the response")
	}

	// It is per-user: alice's own view is unaffected by bob's favorite.
	if got, _ := e.s.GetProject(e.ctx, e.alice, proj.ID); got.Favorite {
		t.Fatal("bob favoriting the project must not favorite it for alice")
	}
	if list, _ := e.s.ListProjects(e.ctx, e.bob, ProjectFilter{}); len(list) != 1 || !list[0].Favorite {
		t.Fatalf("bob's project listing should show it favorited, got %+v", list)
	}

	// Unfavoriting clears it again.
	pr, err = e.s.SetProjectFavorite(e.ctx, e.bob, proj.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if pr.Favorite {
		t.Fatal("favorite should be cleared")
	}

	// A stranger cannot favorite a project they cannot see.
	stranger := e.user("carol", false)
	_, err = e.s.SetProjectFavorite(e.ctx, stranger, proj.ID, true)
	wantCode(t, err, "not_found")
}

func drain2(sub *Subscription) (out []string) {
	for {
		select {
		case ev := <-sub.C:
			out = append(out, ev.Type)
		case <-time.After(150 * time.Millisecond):
			return
		}
	}
}

func contains(list []string, want string) bool {
	for _, x := range list {
		if x == want {
			return true
		}
	}
	return false
}

func TestTaskTypesAreOptionalValidatedFilterableAndSortable(t *testing.T) {
	e := newEnv(t)
	proj := e.project(e.alice, "Typed")
	mk := func(title string, typ *string) *Task {
		return e.task(e.alice, proj.ID, title, func(in *CreateTaskInput) { in.Type = typ })
	}
	bug, feat, plain := mk("a bug", ptr("Bug")), mk("a feature", ptr("feature")), mk("untyped", nil)
	if bug.Type == nil || *bug.Type != "bug" || feat.Type == nil || *feat.Type != "feature" || plain.Type != nil {
		t.Fatalf("types: %v %v %v", bug.Type, feat.Type, plain.Type)
	}
	if m, _ := e.s.GetMeta(e.ctx); len(m.Types) != 3 || m.Types[0].Slug != "improvement" || m.Types[1].Slug != "feature" || m.Types[2].Slug != "bug" {
		t.Fatalf("meta should list Improvement, Feature, then Bug: %+v", m.Types)
	}
	// Unknown types are rejected with the valid values named.
	_, _, err := e.s.CreateTask(e.ctx, e.alice, CreateTaskInput{Project: proj.ID, Title: "x", Type: ptr("epic")})
	wantCode(t, err, "validation_failed")
	if !strings.Contains(err.Error(), "feature") || !strings.Contains(err.Error(), "bug") {
		t.Fatalf("error should list valid types: %v", err)
	}
	// Change, no-op, clear.
	u, err := e.s.UpdateTask(e.ctx, e.alice, plain.ID, UpdateTaskInput{Type: Opt[string]{Set: true, Value: ptr("BUG")}})
	if err != nil || u.Type == nil || *u.Type != "bug" {
		t.Fatalf("set type: %v %+v", err, u)
	}
	same, _ := e.s.UpdateTask(e.ctx, e.alice, plain.ID, UpdateTaskInput{Type: Opt[string]{Set: true, Value: ptr("bug")}})
	if same.Version != u.Version {
		t.Fatal("setting the same type must not bump the version")
	}
	cleared, err := e.s.UpdateTask(e.ctx, e.alice, plain.ID, UpdateTaskInput{Type: Opt[string]{Set: true}})
	if err != nil || cleared.Type != nil {
		t.Fatalf("clear type: %v %+v", err, cleared)
	}
	// Filtering.
	count := func(f TaskFilter) int {
		f.IncludeTotal, f.Limit = true, 1
		pg, err := e.s.ListTasks(e.ctx, e.alice, f)
		if err != nil {
			t.Fatal(err)
		}
		return *pg.Total
	}
	if count(TaskFilter{Types: []string{"bug"}}) != 1 || count(TaskFilter{Types: []string{"feature", "bug"}}) != 2 || count(TaskFilter{Types: []string{"none"}}) != 1 || count(TaskFilter{Types: []string{"none", "bug"}}) != 2 {
		t.Fatal("type filters (bug / feature+bug / none / none+bug) returned wrong counts")
	}
	if _, err := e.s.ListTasks(e.ctx, e.alice, TaskFilter{Types: []string{"epic"}}); err == nil {
		t.Fatal("unknown type in a filter should be an error")
	}
	// Sorting: Improvement, Feature, Bug, then untyped.
	imp := mk("an improvement", ptr("Improvement"))
	if imp.Type == nil || *imp.Type != "improvement" {
		t.Fatalf("improvement type: %v", imp.Type)
	}
	pg, _ := e.s.ListTasks(e.ctx, e.alice, TaskFilter{Sort: "type"})
	if pg.Tasks[0].Title != "an improvement" || pg.Tasks[1].Title != "a feature" || pg.Tasks[2].Title != "a bug" || pg.Tasks[3].Title != "untyped" {
		t.Fatalf("sort by type: %v %v %v %v", pg.Tasks[0].Title, pg.Tasks[1].Title, pg.Tasks[2].Title, pg.Tasks[3].Title)
	}
	// Moving a task keeps its type.
	other := e.project(e.alice, "Other Typed")
	m, err := e.s.UpdateTask(e.ctx, e.alice, bug.ID, UpdateTaskInput{Project: ptr(other.Key)})
	if err != nil || m.Type == nil || *m.Type != "bug" {
		t.Fatalf("move keeps type: %v %+v", err, m)
	}
}

func TestCompletedAtTracksLatestArrivalInDone(t *testing.T) {
	e := newEnv(t)
	proj := e.project(e.alice, "Completion")
	open := e.task(e.alice, proj.ID, "still open")
	if open.CompletedAt != nil {
		t.Fatalf("a new open task must have no completion date, got %v", open.CompletedAt)
	}
	born := e.task(e.alice, proj.ID, "born done", func(in *CreateTaskInput) { in.Status = "done" })
	if born.CompletedAt == nil {
		t.Fatal("a task created as done must have a completion date")
	}

	done := "done"
	first, err := e.s.UpdateTask(e.ctx, e.alice, open.Ref, UpdateTaskInput{Status: &done})
	if err != nil || first.CompletedAt == nil {
		t.Fatalf("moving to done must set completed_at: %v %v", first, err)
	}
	title := "renamed"
	renamed, err := e.s.UpdateTask(e.ctx, e.alice, open.Ref, UpdateTaskInput{Title: &title})
	if err != nil || renamed.CompletedAt == nil || !renamed.CompletedAt.Equal(*first.CompletedAt) {
		t.Fatalf("editing a done task must keep its completion date: %v %v", renamed.CompletedAt, err)
	}
	todo := "todo"
	reopened, err := e.s.UpdateTask(e.ctx, e.alice, open.Ref, UpdateTaskInput{Status: &todo})
	if err != nil || reopened.CompletedAt != nil {
		t.Fatalf("leaving done must clear completed_at: %v %v", reopened.CompletedAt, err)
	}
	again, err := e.s.UpdateTask(e.ctx, e.alice, open.Ref, UpdateTaskInput{Status: &done})
	if err != nil || again.CompletedAt == nil || again.CompletedAt.Before(*first.CompletedAt) {
		t.Fatalf("returning to done must set a fresh completion date: %v %v", again.CompletedAt, err)
	}

	pg, err := e.s.ListTasks(e.ctx, e.alice, TaskFilter{Sort: "-completed_at"})
	if err != nil || len(pg.Tasks) != 2 || pg.Tasks[0].CompletedAt == nil || pg.Tasks[1].CompletedAt == nil {
		t.Fatalf("-completed_at should list completed tasks first: %v %v", pg, err)
	}
}
