// Package mcp exposes LightBacklog to AI agents as an MCP server (streamable HTTP, stateless).
// Every tool is a thin wrapper over the same service methods the REST API uses, so
// validation, permissions and semantics cannot drift between the two.
package mcp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/superbyteone/lightbacklog/internal/ratelimit"
	"github.com/superbyteone/lightbacklog/internal/service"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

const instructions = `LightBacklog task manager. Every task belongs to exactly one project (address projects by key such as "WEB" or by id; tasks by id or ref such as "WEB-42").
Workflow tips:
- Before creating a task, search (search_tasks / list_tasks with q) or pass a stable external_ref to create_task; the same external_ref in a project returns the existing task instead of a duplicate.
- Call get_meta once for valid status and priority values.
- Descriptions are Markdown (GitHub-flavoured: checklists "- [ ] item", code fences, links, images).
- update_task accepts the task's "version"; pass it to avoid overwriting concurrent edits (a version_conflict error returns the current state).
- add_attachment returns a Markdown snippet you can paste into a description to embed images.`

type ctxKey struct{}
type ipCtxKey struct{}

// clientIP returns the raw TCP peer address, without any proxy-header trust: it is only used
// to key the auth-failure limiter below, not for access control.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// realIP returns the client IP a trusted reverse proxy reported via X-Real-IP, or false if none
// was sent or it doesn't parse. X-Forwarded-For is not consulted for this: its left-hand
// entries can be supplied by the client itself unless the proxy is configured to strip them,
// whereas X-Real-IP is expected to be set once, directly, by the proxy from its own view of the
// peer (as the shipped Nginx templates do, from $remote_addr).
func realIP(r *http.Request) (netip.Addr, bool) {
	v := strings.TrimSpace(r.Header.Get("X-Real-IP"))
	if v == "" {
		return netip.Addr{}, false
	}
	ip, err := netip.ParseAddr(v)
	return ip, err == nil
}

func allowedByCIDR(allowed []netip.Prefix, ip netip.Addr) bool {
	for _, p := range allowed {
		if p.Contains(ip) {
			return true
		}
	}
	return false
}

// ParseAllowedCIDRs parses LB_MCP_ALLOWED_CIDRS: a comma-separated list of IPs or CIDRs. An
// empty string returns a nil, empty list -- meaning no proxied request is ever allowed, which
// is the default-safe (local-only) behavior. It rejects entries that would allow every address
// (0.0.0.0/0 or ::/0), since that is almost always a copy-paste mistake rather than an intent
// to disable the allowlist -- LB_MCP_ALLOWED_CIDRS itself being unset already means that.
func ParseAllowedCIDRs(raw string) ([]netip.Prefix, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var out []netip.Prefix
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		var p netip.Prefix
		var err error
		if strings.Contains(part, "/") {
			p, err = netip.ParsePrefix(part)
		} else {
			var ip netip.Addr
			if ip, err = netip.ParseAddr(part); err == nil {
				p = netip.PrefixFrom(ip, ip.BitLen())
			}
		}
		if err != nil {
			return nil, fmt.Errorf("LB_MCP_ALLOWED_CIDRS: invalid entry %q: %w", part, err)
		}
		if p.Bits() == 0 {
			return nil, fmt.Errorf("LB_MCP_ALLOWED_CIDRS: entry %q allows every address; use a specific range", part)
		}
		out = append(out, p)
	}
	return out, nil
}

// Handler returns the HTTP handler for the /mcp endpoint. It authenticates every request with
// an API token, then serves it with a server whose tools are bound to that principal.
// Requests that came through a reverse proxy (X-Forwarded-For / X-Real-IP present) are refused
// unless the resolved client IP (from X-Real-IP) falls within allowedCIDRs; with an empty list,
// every proxied request is refused and agents are expected to connect to localhost directly.
// Two independent limits apply once auth is reached: repeated bad tokens from one peer are
// throttled (authRPS), and each authenticated token is capped at rps requests/sec (burst) so a
// single leaked or malfunctioning credential can't overload the instance. Every tool call is
// recorded by audit (see audit.go); callers must arrange for audit.Run to be started and, on
// shutdown, waited on so buffered entries are flushed.
func Handler(svc *service.Service, version string, allowedCIDRs []netip.Prefix, rps float64, burst int, audit *AuditSink) http.Handler {
	cache := sdk.NewSchemaCache()
	inner := sdk.NewStreamableHTTPHandler(func(r *http.Request) *sdk.Server {
		p, ok := r.Context().Value(ctxKey{}).(service.Principal)
		if !ok {
			return nil
		}
		return newServer(svc, p, version, cache, audit)
	}, &sdk.StreamableHTTPOptions{Stateless: true, JSONResponse: true})

	authFails := ratelimit.NewFailLimiter(10, 10*time.Minute)
	callLimits := newTokenLimiter(rps, burst)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Forwarded-For") != "" || r.Header.Get("X-Real-IP") != "" {
			ip, ok := realIP(r)
			if !ok || !allowedByCIDR(allowedCIDRs, ip) {
				http.Error(w, "the MCP endpoint is not reachable from this network", http.StatusForbidden)
				return
			}
		}
		ipKey := "ip:" + clientIP(r)
		if blocked, wait := authFails.Blocked(ipKey); blocked {
			w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())+1))
			http.Error(w, "too many failed authentication attempts; try again later", http.StatusTooManyRequests)
			return
		}
		tok := ""
		if scheme, v, ok := strings.Cut(r.Header.Get("Authorization"), " "); ok && strings.EqualFold(scheme, "bearer") {
			tok = strings.TrimSpace(v)
		}
		p, err := service.Principal{}, error(nil)
		if tok != "" {
			p, err = svc.AuthenticateToken(r.Context(), tok)
		}
		if tok == "" || err != nil {
			authFails.Fail(ipKey)
			w.Header().Set("WWW-Authenticate", `Bearer realm="lightbacklog"`)
			http.Error(w, "an API token is required: Authorization: Bearer lb_...", http.StatusUnauthorized)
			return
		}
		if !callLimits.allow(p.Token.ID) {
			w.Header().Set("Retry-After", "1")
			http.Error(w, "rate limit exceeded for this token", http.StatusTooManyRequests)
			return
		}
		ctx := context.WithValue(r.Context(), ctxKey{}, p)
		ctx = context.WithValue(ctx, ipCtxKey{}, clientIP(r))
		inner.ServeHTTP(w, r.WithContext(ctx))
	})
}

// toolError renders a service error so an agent can act on it (code, message, fields).
func toolError(err error) error {
	var se *service.Error
	if !errors.As(err, &se) {
		return fmt.Errorf("internal error")
	}
	msg := se.Code + ": " + se.Message
	if len(se.Fields) > 1 {
		parts := make([]string, len(se.Fields))
		for i, f := range se.Fields {
			parts[i] = f.Field + " " + f.Message
		}
		msg += " [" + strings.Join(parts, "; ") + "]"
	}
	if t, ok := se.Current.(*service.Task); ok && t != nil {
		msg += fmt.Sprintf(" (current version is %d; re-read the task with get_task and retry)", t.Version)
	}
	return errors.New(msg)
}

// addTool registers a tool and wraps it so every call is recorded in the audit log, keyed by
// the tool's own Name so the registration and the audit trail can never drift apart.
func addTool[In, Out any](s *sdk.Server, audit *AuditSink, p service.Principal, tool *sdk.Tool, fn func(ctx context.Context, p service.Principal, in In) (Out, error)) {
	sdk.AddTool(s, tool, wrap(tool.Name, audit, p, fn))
}

func wrap[In, Out any](tool string, audit *AuditSink, p service.Principal, fn func(ctx context.Context, p service.Principal, in In) (Out, error)) sdk.ToolHandlerFor[In, any] {
	return func(ctx context.Context, _ *sdk.CallToolRequest, in In) (*sdk.CallToolResult, any, error) {
		start := time.Now()
		out, err := fn(ctx, p, in)
		audit.record(auditEntryFor(ctx, p, tool, start, err))
		if err != nil {
			return nil, nil, toolError(err)
		}
		return nil, out, nil
	}
}

// auditEntryFor builds the audit row for one tool call from its principal, timing and outcome.
func auditEntryFor(ctx context.Context, p service.Principal, tool string, start time.Time, err error) service.MCPAuditEntry {
	e := service.MCPAuditEntry{UserID: p.UserID, Tool: tool, Status: "ok", DurationMS: time.Since(start).Milliseconds(), CreatedAt: time.Now()}
	if p.Token != nil {
		e.TokenID = p.Token.ID
	}
	if ip, ok := ctx.Value(ipCtxKey{}).(string); ok {
		e.ClientIP = ip
	}
	if err != nil {
		e.Status = "error"
		var se *service.Error
		if errors.As(err, &se) {
			e.ErrorCode = se.Code
		}
	}
	return e
}

func readOnly(title string) *sdk.ToolAnnotations {
	return &sdk.ToolAnnotations{Title: title, ReadOnlyHint: true}
}

func writes(title string, idempotent bool) *sdk.ToolAnnotations {
	f := false
	return &sdk.ToolAnnotations{Title: title, DestructiveHint: &f, IdempotentHint: idempotent}
}

// --- tool inputs ----------------------------------------------------------------------

type listProjectsIn struct {
	Query    string `json:"query,omitempty" jsonschema:"case-insensitive text to match in project name, key or description"`
	Archived string `json:"archived,omitempty" jsonschema:"active (default), archived or all"`
}

type projectRefIn struct {
	Project string `json:"project" jsonschema:"project key (e.g. WEB) or id"`
}

type createProjectIn struct {
	Name        string `json:"name" jsonschema:"project name"`
	Key         string `json:"key,omitempty" jsonschema:"2-8 letters/digits starting with a letter; derived from the name when omitted"`
	Description string `json:"description,omitempty"`
	Color       string `json:"color,omitempty" jsonschema:"#rrggbb"`
}

type updateProjectIn struct {
	Project     string  `json:"project" jsonschema:"project key or id"`
	Name        *string `json:"name,omitempty"`
	Description *string `json:"description,omitempty"`
	Color       *string `json:"color,omitempty" jsonschema:"#rrggbb"`
}

type archiveProjectIn struct {
	Project  string `json:"project" jsonschema:"project key or id"`
	Archived *bool  `json:"archived,omitempty" jsonschema:"true (default) archives the project, false restores it"`
}

type favoriteProjectIn struct {
	Project  string `json:"project" jsonschema:"project key or id"`
	Favorite *bool  `json:"favorite,omitempty" jsonschema:"true (default) stars the project for you, false unstars it"`
}

type listTasksIn struct {
	Project            []string `json:"project,omitempty" jsonschema:"restrict to these project keys or ids"`
	Status             []string `json:"status,omitempty" jsonschema:"todo, in_progress, blocked, done"`
	Priority           []string `json:"priority,omitempty" jsonschema:"high, medium, low"`
	Type               []string `json:"type,omitempty" jsonschema:"improvement, feature, bug, or none for tasks without a type"`
	Label              []string `json:"label,omitempty" jsonschema:"label names; tasks having any of them (or all with label_match=all)"`
	LabelMatch         string   `json:"label_match,omitempty" jsonschema:"any (default) or all"`
	Query              string   `json:"query,omitempty" jsonschema:"full-text search over title and description, or a task ref like WEB-42"`
	DueBefore          string   `json:"due_before,omitempty" jsonschema:"YYYY-MM-DD, inclusive"`
	DueAfter           string   `json:"due_after,omitempty" jsonschema:"YYYY-MM-DD, inclusive"`
	Sort               string   `json:"sort,omitempty" jsonschema:"updated_at (default, newest first), created_at, completed_at, title, due_date, priority, status, project, position, sequence, relevance; prefix with - for descending"`
	Limit              int      `json:"limit,omitempty" jsonschema:"page size, default 50, max 200"`
	Cursor             string   `json:"cursor,omitempty" jsonschema:"next_cursor from a previous page"`
	IncludeDescription bool     `json:"include_description,omitempty"`
	IncludeArchived    bool     `json:"include_archived,omitempty" jsonschema:"also search tasks of archived projects"`
}

type searchTasksIn struct {
	Query   string   `json:"query" jsonschema:"words to search for in title and description (prefix matching), or a task ref like WEB-42"`
	Project []string `json:"project,omitempty"`
	Status  []string `json:"status,omitempty"`
	Limit   int      `json:"limit,omitempty" jsonschema:"default 20"`
}

type taskRefIn struct {
	Task string `json:"task" jsonschema:"task id or ref such as WEB-42"`
}

type createTaskIn struct {
	Project             string   `json:"project" jsonschema:"project key or id (required)"`
	Title               string   `json:"title"`
	Description         string   `json:"description,omitempty" jsonschema:"Markdown"`
	Status              string   `json:"status,omitempty" jsonschema:"todo (default), in_progress, blocked, done"`
	Priority            string   `json:"priority,omitempty" jsonschema:"high, medium (default), low"`
	DueDate             string   `json:"due_date,omitempty" jsonschema:"YYYY-MM-DD"`
	Labels              []string `json:"labels,omitempty" jsonschema:"label names; missing labels are created unless create_missing_labels is false"`
	CreateMissingLabels *bool    `json:"create_missing_labels,omitempty"`
	Type                string   `json:"type,omitempty" jsonschema:"optional kind of task: improvement, feature or bug"`
	Sequence            *int     `json:"sequence,omitempty" jsonschema:"optional work order as a whole number: 1 is tackled first, 2 next, and so on"`
	ExternalRef         string   `json:"external_ref,omitempty" jsonschema:"your own stable identifier (e.g. an issue URL); re-creating with the same value in the same project returns the existing task"`
}

type updateTaskIn struct {
	Task            string    `json:"task" jsonschema:"task id or ref such as WEB-42"`
	Title           *string   `json:"title,omitempty"`
	Description     *string   `json:"description,omitempty" jsonschema:"Markdown; replaces the whole description"`
	Status          *string   `json:"status,omitempty"`
	Priority        *string   `json:"priority,omitempty"`
	DueDate         string    `json:"due_date,omitempty" jsonschema:"YYYY-MM-DD"`
	ClearDueDate    bool      `json:"clear_due_date,omitempty"`
	Labels          *[]string `json:"labels,omitempty" jsonschema:"replaces the full label set"`
	AddLabels       []string  `json:"add_labels,omitempty"`
	RemoveLabels    []string  `json:"remove_labels,omitempty"`
	ExternalRef     *string   `json:"external_ref,omitempty"`
	ClearExtRef     bool      `json:"clear_external_ref,omitempty"`
	Type            *string   `json:"type,omitempty" jsonschema:"improvement, feature or bug"`
	ClearType       bool      `json:"clear_type,omitempty"`
	Sequence        *int      `json:"sequence,omitempty" jsonschema:"work order: a whole number, 1 is tackled first"`
	ClearSequence   bool      `json:"clear_sequence,omitempty"`
	ExpectedVersion *int64    `json:"version,omitempty" jsonschema:"the task version you last read; the update fails with version_conflict if it changed"`
}

type moveTaskIn struct {
	Task    string `json:"task" jsonschema:"task id or ref"`
	Project string `json:"project" jsonschema:"destination project key or id"`
}

type deleteTaskIn struct {
	Task    string `json:"task" jsonschema:"task id or ref"`
	Version *int64 `json:"version,omitempty" jsonschema:"optional version guard"`
}

type createLabelIn struct {
	Name  string `json:"name"`
	Color string `json:"color,omitempty" jsonschema:"#rrggbb"`
}

type addAttachmentIn struct {
	Task          string `json:"task" jsonschema:"task id or ref"`
	Filename      string `json:"filename" jsonschema:"file name including extension"`
	ContentBase64 string `json:"content_base64" jsonschema:"file content, base64 encoded (standard alphabet); images (png/jpeg/gif/webp), pdf, zip, gzip and text are accepted"`
}

type emptyIn struct{}

type pageOut struct {
	Tasks      []*service.Task `json:"tasks"`
	NextCursor string          `json:"next_cursor,omitempty"`
	Total      *int            `json:"total,omitempty"`
}

type attachmentOut struct {
	*service.Attachment
	Markdown string `json:"markdown"`
}

func newServer(svc *service.Service, p service.Principal, version string, cache *sdk.SchemaCache, audit *AuditSink) *sdk.Server {
	s := sdk.NewServer(&sdk.Implementation{Name: "lightbacklog", Title: "LightBacklog", Version: version},
		&sdk.ServerOptions{Instructions: instructions, SchemaCache: cache})

	addTool(s, audit, p, &sdk.Tool{Name: "get_meta", Description: "List the valid statuses and priorities (with display order).", Annotations: readOnly("Get statuses and priorities")},
		func(ctx context.Context, p service.Principal, _ emptyIn) (*service.Meta, error) {
			return svc.GetMeta(ctx)
		})

	addTool(s, audit, p, &sdk.Tool{Name: "list_projects", Description: "List the projects you can access, alphabetically, with your role and open/total task counts. Use `query` to search.", Annotations: readOnly("List projects")},
		func(ctx context.Context, p service.Principal, in listProjectsIn) (map[string]any, error) {
			ps, err := svc.ListProjects(ctx, p, service.ProjectFilter{Q: in.Query, Archived: in.Archived})
			return map[string]any{"projects": ps}, err
		})
	addTool(s, audit, p, &sdk.Tool{Name: "get_project", Description: "Get one project by key or id.", Annotations: readOnly("Get project")},
		func(ctx context.Context, p service.Principal, in projectRefIn) (*service.Project, error) {
			return svc.GetProject(ctx, p, in.Project)
		})
	addTool(s, audit, p, &sdk.Tool{Name: "create_project", Description: "Create a project; you become its owner.", Annotations: writes("Create project", false)},
		func(ctx context.Context, p service.Principal, in createProjectIn) (*service.Project, error) {
			return svc.CreateProject(ctx, p, service.CreateProjectInput{Key: in.Key, Name: in.Name, Description: in.Description, Color: in.Color})
		})
	addTool(s, audit, p, &sdk.Tool{Name: "update_project", Description: "Change a project's name, description or color (owner only). The key is immutable.", Annotations: writes("Update project", true)},
		func(ctx context.Context, p service.Principal, in updateProjectIn) (*service.Project, error) {
			return svc.UpdateProject(ctx, p, in.Project, service.UpdateProjectInput{Name: in.Name, Description: in.Description, Color: in.Color})
		})
	addTool(s, audit, p, &sdk.Tool{Name: "archive_project", Description: "Archive a project (hidden from default lists, tasks become read-only) or restore it with archived=false (owner only).", Annotations: writes("Archive project", true)},
		func(ctx context.Context, p service.Principal, in archiveProjectIn) (*service.Project, error) {
			return svc.SetProjectArchived(ctx, p, in.Project, in.Archived == nil || *in.Archived)
		})
	addTool(s, audit, p, &sdk.Tool{Name: "favorite_project", Description: "Star a project for yourself, or unstar it with favorite=false. A personal preference, not project-wide; any member may set it.", Annotations: writes("Favorite project", true)},
		func(ctx context.Context, p service.Principal, in favoriteProjectIn) (*service.Project, error) {
			return svc.SetProjectFavorite(ctx, p, in.Project, in.Favorite == nil || *in.Favorite)
		})

	addTool(s, audit, p, &sdk.Tool{Name: "list_tasks", Description: "List tasks across all your projects (or filtered by project, status, priority, labels, due dates, text). Descriptions are omitted unless include_description is set. Paginate with next_cursor.", Annotations: readOnly("List tasks")},
		func(ctx context.Context, p service.Principal, in listTasksIn) (*pageOut, error) {
			page, err := svc.ListTasks(ctx, p, service.TaskFilter{
				Projects: in.Project, Statuses: in.Status, Priorities: in.Priority, Types: in.Type, Labels: in.Label, LabelMatch: in.LabelMatch, Q: in.Query,
				DueBefore: in.DueBefore, DueAfter: in.DueAfter, Sort: in.Sort, Limit: in.Limit, Cursor: in.Cursor,
				IncludeDescription: in.IncludeDescription, IncludeArchived: in.IncludeArchived, IncludeTotal: in.Cursor == "",
			})
			if err != nil {
				return nil, err
			}
			return &pageOut{Tasks: page.Tasks, NextCursor: page.NextCursor, Total: page.Total}, nil
		})
	addTool(s, audit, p, &sdk.Tool{Name: "search_tasks", Description: "Full-text search over task titles and descriptions, best matches first. Also accepts a task ref (WEB-42).", Annotations: readOnly("Search tasks")},
		func(ctx context.Context, p service.Principal, in searchTasksIn) (*pageOut, error) {
			limit := in.Limit
			if limit <= 0 {
				limit = 20
			}
			page, err := svc.ListTasks(ctx, p, service.TaskFilter{Q: in.Query, Projects: in.Project, Statuses: in.Status, Limit: limit, Sort: "relevance"})
			if err != nil {
				return nil, err
			}
			return &pageOut{Tasks: page.Tasks}, nil
		})
	addTool(s, audit, p, &sdk.Tool{Name: "get_task", Description: "Get a task with its full description, labels and version.", Annotations: readOnly("Get task")},
		func(ctx context.Context, p service.Principal, in taskRefIn) (*service.Task, error) {
			return svc.GetTask(ctx, p, in.Task)
		})
	addTool(s, audit, p, &sdk.Tool{Name: "create_task", Description: "Create a task in a project. Pass a stable external_ref to make retries safe: an existing task with the same external_ref in that project is returned unchanged.", Annotations: writes("Create task", false)},
		func(ctx context.Context, p service.Principal, in createTaskIn) (map[string]any, error) {
			ci := service.CreateTaskInput{Project: in.Project, Title: in.Title, Description: in.Description, Status: in.Status, Priority: in.Priority,
				Labels: in.Labels, CreateMissingLabels: in.CreateMissingLabels, Sequence: in.Sequence}
			if in.Type != "" {
				ci.Type = &in.Type
			}
			if in.DueDate != "" {
				ci.DueDate = &in.DueDate
			}
			if in.ExternalRef != "" {
				ci.ExternalRef = &in.ExternalRef
			}
			t, created, err := svc.CreateTask(ctx, p, ci)
			return map[string]any{"task": t, "created": created}, err
		})
	addTool(s, audit, p, &sdk.Tool{Name: "update_task", Description: "Partially update a task: only the fields you pass change. Use add_labels/remove_labels to adjust labels without reading first, and pass version to detect concurrent edits.", Annotations: writes("Update task", true)},
		func(ctx context.Context, p service.Principal, in updateTaskIn) (*service.Task, error) {
			ui := service.UpdateTaskInput{Title: in.Title, Description: in.Description, Status: in.Status, Priority: in.Priority, Labels: in.Labels,
				AddLabels: in.AddLabels, RemoveLabels: in.RemoveLabels, Version: in.ExpectedVersion}
			switch {
			case in.ClearDueDate:
				ui.DueDate.Set = true
			case in.DueDate != "":
				ui.DueDate.Set, ui.DueDate.Value = true, &in.DueDate
			}
			switch {
			case in.ClearType:
				ui.Type.Set = true
			case in.Type != nil:
				ui.Type.Set, ui.Type.Value = true, in.Type
			}
			switch {
			case in.ClearSequence:
				ui.Sequence.Set = true
			case in.Sequence != nil:
				ui.Sequence.Set, ui.Sequence.Value = true, in.Sequence
			}
			switch {
			case in.ClearExtRef:
				ui.ExternalRef.Set = true
			case in.ExternalRef != nil:
				ui.ExternalRef.Set, ui.ExternalRef.Value = true, in.ExternalRef
			}
			return svc.UpdateTask(ctx, p, in.Task, ui)
		})
	addTool(s, audit, p, &sdk.Tool{Name: "move_task", Description: "Move a task to another project. Its id is unchanged; its ref (KEY-number) changes to the destination project's numbering.", Annotations: writes("Move task", true)},
		func(ctx context.Context, p service.Principal, in moveTaskIn) (*service.Task, error) {
			return svc.UpdateTask(ctx, p, in.Task, service.UpdateTaskInput{Project: &in.Project})
		})
	f := true
	addTool(s, audit, p, &sdk.Tool{Name: "delete_task", Description: "Permanently delete a task and its attachments. This cannot be undone.", Annotations: &sdk.ToolAnnotations{Title: "Delete task", DestructiveHint: &f}},
		func(ctx context.Context, p service.Principal, in deleteTaskIn) (map[string]any, error) {
			return map[string]any{"deleted": true}, svc.DeleteTask(ctx, p, in.Task, in.Version)
		})

	addTool(s, audit, p, &sdk.Tool{Name: "list_labels", Description: "List all labels (shared across projects).", Annotations: readOnly("List labels")},
		func(ctx context.Context, p service.Principal, _ emptyIn) (map[string]any, error) {
			ls, err := svc.ListLabels(ctx, p)
			return map[string]any{"labels": ls}, err
		})
	addTool(s, audit, p, &sdk.Tool{Name: "create_label", Description: "Create a label, or return the existing one with that name (case-insensitive).", Annotations: writes("Create label", true)},
		func(ctx context.Context, p service.Principal, in createLabelIn) (*service.Label, error) {
			return svc.CreateLabel(ctx, p, in.Name, in.Color)
		})

	addTool(s, audit, p, &sdk.Tool{Name: "add_attachment", Description: "Attach a file or screenshot to a task. Returns a Markdown snippet (image or link) you can embed in the description via update_task.", Annotations: writes("Add attachment", false)},
		func(ctx context.Context, p service.Principal, in addAttachmentIn) (*attachmentOut, error) {
			raw := base64.NewDecoder(base64.StdEncoding, strings.NewReader(strings.TrimSpace(in.ContentBase64)))
			a, err := svc.Upload(ctx, p, "", in.Task, in.Filename, raw)
			if err != nil {
				var se *service.Error
				if !errors.As(err, &se) {
					return nil, &service.Error{Status: 422, Code: "validation_failed", Message: "content_base64 is not valid base64"}
				}
				return nil, err
			}
			return &attachmentOut{Attachment: a, Markdown: markdown(a)}, nil
		})
	addTool(s, audit, p, &sdk.Tool{Name: "list_attachments", Description: "List a task's attachments.", Annotations: readOnly("List attachments")},
		func(ctx context.Context, p service.Principal, in taskRefIn) (map[string]any, error) {
			as, err := svc.ListAttachments(ctx, p, in.Task)
			out := make([]attachmentOut, len(as))
			for i := range as {
				out[i] = attachmentOut{Attachment: &as[i], Markdown: markdown(&as[i])}
			}
			return map[string]any{"attachments": out}, err
		})
	return s
}

func markdown(a *service.Attachment) string {
	name := strings.NewReplacer("[", "(", "]", ")").Replace(a.Filename)
	if a.Inline {
		return "![" + name + "](" + a.URL + ")"
	}
	return "[" + name + "](" + a.URL + ")"
}

var _ = json.Marshal
