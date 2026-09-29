package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/superbyteone/lightbacklog/internal/service"
)

func (a *API) routeTable() {
	a.open("GET /healthz", a.healthz)
	a.open("GET /api/v1/openapi.yaml", a.openapi)

	a.open("POST /api/v1/auth/login", a.loginHandler)
	a.secured("POST /api/v1/auth/logout", a.logout)
	a.secured("GET /api/v1/me", a.me)
	a.secured("POST /api/v1/me/password", a.changePassword)
	a.secured("GET /api/v1/me/preferences", a.getPreferences)
	a.secured("PATCH /api/v1/me/preferences", a.patchPreferences)

	a.secured("GET /api/v1/users", a.listUsers)
	a.secured("POST /api/v1/users", a.createUser)
	a.secured("POST /api/v1/users/{id}/disable", a.disableUser)
	a.secured("POST /api/v1/users/{id}/enable", a.enableUser)

	a.secured("GET /api/v1/tokens", a.listTokens)
	a.secured("POST /api/v1/tokens", a.createToken)
	a.secured("DELETE /api/v1/tokens/{id}", a.revokeToken)
	a.secured("POST /api/v1/tokens/{id}/rotate", a.rotateToken)

	a.secured("GET /api/v1/meta", a.meta)
	a.secured("GET /api/v1/labels", a.listLabels)
	a.secured("POST /api/v1/labels", a.createLabel)
	a.secured("PATCH /api/v1/labels/{id}", a.updateLabel)
	a.secured("DELETE /api/v1/labels/{id}", a.deleteLabel)

	a.secured("GET /api/v1/projects", a.listProjects)
	a.secured("POST /api/v1/projects", a.createProject)
	a.secured("GET /api/v1/projects/{ref}", a.getProject)
	a.secured("PATCH /api/v1/projects/{ref}", a.updateProject)
	a.secured("DELETE /api/v1/projects/{ref}", a.deleteProject)
	a.secured("POST /api/v1/projects/{ref}/archive", a.archiveProject)
	a.secured("POST /api/v1/projects/{ref}/unarchive", a.unarchiveProject)
	a.secured("POST /api/v1/projects/{ref}/favorite", a.favoriteProject)
	a.secured("POST /api/v1/projects/{ref}/unfavorite", a.unfavoriteProject)
	a.secured("GET /api/v1/projects/{ref}/members", a.listMembers)
	a.secured("PUT /api/v1/projects/{ref}/members/{username}", a.setMember)
	a.secured("DELETE /api/v1/projects/{ref}/members/{userID}", a.removeMember)
	a.secured("GET /api/v1/projects/{ref}/tasks", a.listProjectTasks)

	a.secured("GET /api/v1/tasks", a.listTasks)
	a.secured("POST /api/v1/tasks", a.createTask)
	a.secured("GET /api/v1/tasks/{ref}", a.getTask)
	a.secured("PATCH /api/v1/tasks/{ref}", a.updateTask)
	a.secured("DELETE /api/v1/tasks/{ref}", a.deleteTask)
	a.secured("POST /api/v1/tasks/{ref}/move", a.moveTask)
	a.secured("GET /api/v1/tasks/{ref}/attachments", a.listAttachments)
	a.secured("POST /api/v1/tasks/{ref}/attachments", a.uploadToTask)

	a.secured("POST /api/v1/uploads", a.uploadToProject)
	a.secured("DELETE /api/v1/attachments/{id}", a.deleteAttachment)
	a.secured("GET /files/{id}", a.serveFile)
	a.secured("GET /api/v1/events", a.events)
}

func (a *API) healthz(w http.ResponseWriter, r *http.Request) error {
	if err := a.svc.Ping(r.Context()); err != nil {
		return &service.Error{Status: http.StatusServiceUnavailable, Code: "unavailable", Message: "database unavailable"}
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "version": a.cfg.Version})
	return nil
}

func (a *API) openapi(w http.ResponseWriter, r *http.Request) error {
	w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
	_, _ = w.Write(a.cfg.OpenAPI)
	return nil
}

// --- auth -----------------------------------------------------------------------------

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func (a *API) loginHandler(w http.ResponseWriter, r *http.Request) error {
	var in loginRequest
	if err := decode(w, r, &in); err != nil {
		return err
	}
	if err := a.checkCSRF(r); err != nil {
		return err
	}
	ipKey, userKey := "ip:"+a.clientIP(r), "user:"+strings.ToLower(strings.TrimSpace(in.Username))
	for _, k := range []string{ipKey, userKey} {
		if blocked, wait := a.login.Blocked(k); blocked {
			w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())+1))
			return &service.Error{Status: http.StatusTooManyRequests, Code: "rate_limited", Message: "too many failed login attempts; try again later"}
		}
	}
	sess, err := a.svc.Login(r.Context(), in.Username, in.Password, r.UserAgent())
	if err != nil {
		if se, ok := err.(*service.Error); ok && se.Code == "invalid_credentials" {
			a.login.Fail(ipKey)
			a.login.Fail(userKey)
		}
		return err
	}
	a.login.Reset(userKey)
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: sess.Secret, Path: "/", Expires: sess.ExpiresAt,
		HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: a.cfg.CookieSecure || a.isHTTPS(r),
	})
	ok(w, sess.User)
	return nil
}

func (a *API) logout(w http.ResponseWriter, r *http.Request, p service.Principal) error {
	if c, err := r.Cookie(sessionCookie); err == nil {
		_ = a.svc.Logout(r.Context(), c.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: a.cfg.CookieSecure || a.isHTTPS(r)})
	w.WriteHeader(http.StatusNoContent)
	return nil
}

type meResponse struct {
	*service.User
	Auth       string   `json:"auth"` // "session" or "token"
	Scope      string   `json:"scope,omitempty"`
	ProjectIDs []string `json:"token_project_ids,omitempty"`
}

func (a *API) me(w http.ResponseWriter, r *http.Request, p service.Principal) error {
	u, err := a.svc.GetUser(r.Context(), p)
	if err != nil {
		return err
	}
	resp := meResponse{User: u, Auth: "session"}
	if p.Token != nil {
		resp.Auth, resp.Scope, resp.ProjectIDs = "token", p.Token.Scope, p.Token.ProjectIDs
		resp.IsAdmin = false
	}
	ok(w, resp)
	return nil
}

type passwordRequest struct {
	Current string `json:"current_password"`
	New     string `json:"new_password"`
}

func (a *API) changePassword(w http.ResponseWriter, r *http.Request, p service.Principal) error {
	var in passwordRequest
	if err := decode(w, r, &in); err != nil {
		return err
	}
	c, _ := r.Cookie(sessionCookie)
	secret := ""
	if c != nil {
		secret = c.Value
	}
	if err := a.svc.ChangePassword(r.Context(), p, in.Current, in.New, secret); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (a *API) listUsers(w http.ResponseWriter, r *http.Request, p service.Principal) error {
	us, err := a.svc.ListUsers(r.Context(), p)
	if err != nil {
		return err
	}
	ok(w, us)
	return nil
}

func (a *API) createUser(w http.ResponseWriter, r *http.Request, p service.Principal) error {
	var in service.CreateUserInput
	if err := decode(w, r, &in); err != nil {
		return err
	}
	u, err := a.svc.AdminCreateUser(r.Context(), p, in)
	if err != nil {
		return err
	}
	created(w, u)
	return nil
}

func (a *API) disableUser(w http.ResponseWriter, r *http.Request, p service.Principal) error {
	if err := a.svc.SetUserDisabled(r.Context(), p, r.PathValue("id"), true); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (a *API) enableUser(w http.ResponseWriter, r *http.Request, p service.Principal) error {
	if err := a.svc.SetUserDisabled(r.Context(), p, r.PathValue("id"), false); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// --- tokens ---------------------------------------------------------------------------

type createTokenResponse struct {
	*service.APIToken
	Secret string `json:"secret"` // shown once
}

func (a *API) listTokens(w http.ResponseWriter, r *http.Request, p service.Principal) error {
	ts, err := a.svc.ListTokens(r.Context(), p)
	if err != nil {
		return err
	}
	ok(w, ts)
	return nil
}

func (a *API) createToken(w http.ResponseWriter, r *http.Request, p service.Principal) error {
	var in service.CreateTokenInput
	if err := decode(w, r, &in); err != nil {
		return err
	}
	t, secret, err := a.svc.CreateToken(r.Context(), p, in)
	if err != nil {
		return err
	}
	created(w, createTokenResponse{APIToken: t, Secret: secret})
	return nil
}

func (a *API) revokeToken(w http.ResponseWriter, r *http.Request, p service.Principal) error {
	if err := a.svc.RevokeToken(r.Context(), p, r.PathValue("id")); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (a *API) rotateToken(w http.ResponseWriter, r *http.Request, p service.Principal) error {
	t, secret, err := a.svc.RotateToken(r.Context(), p, r.PathValue("id"))
	if err != nil {
		return err
	}
	created(w, createTokenResponse{APIToken: t, Secret: secret})
	return nil
}

// --- meta & labels --------------------------------------------------------------------

func (a *API) meta(w http.ResponseWriter, r *http.Request, p service.Principal) error {
	m, err := a.svc.GetMeta(r.Context())
	if err != nil {
		return err
	}
	ok(w, m)
	return nil
}

func (a *API) listLabels(w http.ResponseWriter, r *http.Request, p service.Principal) error {
	ls, err := a.svc.ListLabels(r.Context(), p)
	if err != nil {
		return err
	}
	ok(w, ls)
	return nil
}

type labelRequest struct {
	Name  *string `json:"name"`
	Color *string `json:"color"`
}

func (a *API) createLabel(w http.ResponseWriter, r *http.Request, p service.Principal) error {
	var in labelRequest
	if err := decode(w, r, &in); err != nil {
		return err
	}
	name, color := "", ""
	if in.Name != nil {
		name = *in.Name
	}
	if in.Color != nil {
		color = *in.Color
	}
	l, err := a.svc.CreateLabel(r.Context(), p, name, color)
	if err != nil {
		return err
	}
	created(w, l)
	return nil
}

func (a *API) updateLabel(w http.ResponseWriter, r *http.Request, p service.Principal) error {
	var in labelRequest
	if err := decode(w, r, &in); err != nil {
		return err
	}
	l, err := a.svc.UpdateLabel(r.Context(), p, r.PathValue("id"), in.Name, in.Color)
	if err != nil {
		return err
	}
	ok(w, l)
	return nil
}

func (a *API) deleteLabel(w http.ResponseWriter, r *http.Request, p service.Principal) error {
	if err := a.svc.DeleteLabel(r.Context(), p, r.PathValue("id")); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// --- projects -------------------------------------------------------------------------

func (a *API) listProjects(w http.ResponseWriter, r *http.Request, p service.Principal) error {
	q := r.URL.Query()
	ps, err := a.svc.ListProjects(r.Context(), p, service.ProjectFilter{Q: q.Get("q"), Archived: q.Get("archived")})
	if err != nil {
		return err
	}
	ok(w, ps)
	return nil
}

func (a *API) createProject(w http.ResponseWriter, r *http.Request, p service.Principal) error {
	return a.idempotent(w, r, p, func(body []byte) (int, any, error) {
		var in service.CreateProjectInput
		if err := decodeBytes(body, &in); err != nil {
			return 0, nil, err
		}
		pr, err := a.svc.CreateProject(r.Context(), p, in)
		if err != nil {
			return 0, nil, err
		}
		w.Header().Set("Location", "/api/v1/projects/"+pr.ID)
		return http.StatusCreated, pr, nil
	})
}

func (a *API) getProject(w http.ResponseWriter, r *http.Request, p service.Principal) error {
	pr, err := a.svc.GetProject(r.Context(), p, r.PathValue("ref"))
	if err != nil {
		return err
	}
	ok(w, pr)
	return nil
}

func (a *API) updateProject(w http.ResponseWriter, r *http.Request, p service.Principal) error {
	var in service.UpdateProjectInput
	if err := decode(w, r, &in); err != nil {
		return err
	}
	pr, err := a.svc.UpdateProject(r.Context(), p, r.PathValue("ref"), in)
	if err != nil {
		return err
	}
	ok(w, pr)
	return nil
}

func (a *API) archiveProject(w http.ResponseWriter, r *http.Request, p service.Principal) error {
	pr, err := a.svc.SetProjectArchived(r.Context(), p, r.PathValue("ref"), true)
	if err != nil {
		return err
	}
	ok(w, pr)
	return nil
}

func (a *API) unarchiveProject(w http.ResponseWriter, r *http.Request, p service.Principal) error {
	pr, err := a.svc.SetProjectArchived(r.Context(), p, r.PathValue("ref"), false)
	if err != nil {
		return err
	}
	ok(w, pr)
	return nil
}

func (a *API) favoriteProject(w http.ResponseWriter, r *http.Request, p service.Principal) error {
	pr, err := a.svc.SetProjectFavorite(r.Context(), p, r.PathValue("ref"), true)
	if err != nil {
		return err
	}
	ok(w, pr)
	return nil
}

func (a *API) unfavoriteProject(w http.ResponseWriter, r *http.Request, p service.Principal) error {
	pr, err := a.svc.SetProjectFavorite(r.Context(), p, r.PathValue("ref"), false)
	if err != nil {
		return err
	}
	ok(w, pr)
	return nil
}

func (a *API) listMembers(w http.ResponseWriter, r *http.Request, p service.Principal) error {
	ms, err := a.svc.ListMembers(r.Context(), p, r.PathValue("ref"))
	if err != nil {
		return err
	}
	ok(w, ms)
	return nil
}

type memberRequest struct {
	Role service.Role `json:"role"`
}

func (a *API) setMember(w http.ResponseWriter, r *http.Request, p service.Principal) error {
	var in memberRequest
	if err := decode(w, r, &in); err != nil {
		return err
	}
	if err := a.svc.SetMember(r.Context(), p, r.PathValue("ref"), r.PathValue("username"), in.Role); err != nil {
		return err
	}
	ms, err := a.svc.ListMembers(r.Context(), p, r.PathValue("ref"))
	if err != nil {
		return err
	}
	ok(w, ms)
	return nil
}

func (a *API) removeMember(w http.ResponseWriter, r *http.Request, p service.Principal) error {
	if err := a.svc.RemoveMember(r.Context(), p, r.PathValue("ref"), r.PathValue("userID")); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// --- tasks ----------------------------------------------------------------------------

// multi collects repeated and comma-separated query values: ?status=todo&status=blocked,done
func multi(r *http.Request, key string) []string {
	var out []string
	for _, v := range r.URL.Query()[key] {
		for _, part := range strings.Split(v, ",") {
			if part = strings.TrimSpace(part); part != "" {
				out = append(out, part)
			}
		}
	}
	return out
}

func queryBool(r *http.Request, key string) (*bool, error) {
	v := r.URL.Query().Get(key)
	if v == "" {
		return nil, nil
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return nil, badRequest("invalid_query", key+" must be true or false")
	}
	return &b, nil
}

func taskFilterFromQuery(r *http.Request) (service.TaskFilter, error) {
	q := r.URL.Query()
	f := service.TaskFilter{
		Projects: multi(r, "project"), Statuses: multi(r, "status"), Types: multi(r, "type"), Priorities: multi(r, "priority"), Labels: multi(r, "label"),
		LabelMatch: q.Get("label_match"), Q: q.Get("q"), DueBefore: q.Get("due_before"), DueAfter: q.Get("due_after"),
		Sort: q.Get("sort"), Cursor: q.Get("cursor"),
	}
	if l := q.Get("limit"); l != "" {
		n, err := strconv.Atoi(l)
		if err != nil || n < 1 {
			return f, badRequest("invalid_query", "limit must be a positive integer")
		}
		f.Limit = n
	}
	var err error
	if f.HasDue, err = queryBool(r, "has_due"); err != nil {
		return f, err
	}
	for key, dst := range map[string]*bool{"include_archived": &f.IncludeArchived, "include_description": &f.IncludeDescription, "include_total": &f.IncludeTotal} {
		b, err := queryBool(r, key)
		if err != nil {
			return f, err
		}
		if b != nil {
			*dst = *b
		}
	}
	return f, nil
}

func (a *API) writeTaskPage(w http.ResponseWriter, page *service.TaskPage) {
	writeJSON(w, http.StatusOK, envelope{Data: page.Tasks, NextCursor: page.NextCursor, Total: page.Total})
}

func (a *API) listTasks(w http.ResponseWriter, r *http.Request, p service.Principal) error {
	f, err := taskFilterFromQuery(r)
	if err != nil {
		return err
	}
	page, err := a.svc.ListTasks(r.Context(), p, f)
	if err != nil {
		return err
	}
	a.writeTaskPage(w, page)
	return nil
}

func (a *API) listProjectTasks(w http.ResponseWriter, r *http.Request, p service.Principal) error {
	f, err := taskFilterFromQuery(r)
	if err != nil {
		return err
	}
	f.Projects = []string{r.PathValue("ref")}
	// Verify access up front so an unknown project is a 404 rather than an empty list.
	if _, err := a.svc.GetProject(r.Context(), p, r.PathValue("ref")); err != nil {
		return err
	}
	page, err := a.svc.ListTasks(r.Context(), p, f)
	if err != nil {
		return err
	}
	a.writeTaskPage(w, page)
	return nil
}

func etag(v int64) string { return `"` + strconv.FormatInt(v, 10) + `"` }

func (a *API) createTask(w http.ResponseWriter, r *http.Request, p service.Principal) error {
	return a.idempotent(w, r, p, func(body []byte) (int, any, error) {
		var in service.CreateTaskInput
		if err := decodeBytes(body, &in); err != nil {
			return 0, nil, err
		}
		t, isNew, err := a.svc.CreateTask(r.Context(), p, in)
		if err != nil {
			return 0, nil, err
		}
		w.Header().Set("Location", "/api/v1/tasks/"+t.ID)
		w.Header().Set("ETag", etag(t.Version))
		if !isNew {
			w.Header().Set("X-Existing-Task", "true")
			return http.StatusOK, t, nil
		}
		return http.StatusCreated, t, nil
	})
}

func (a *API) getTask(w http.ResponseWriter, r *http.Request, p service.Principal) error {
	t, err := a.svc.GetTask(r.Context(), p, r.PathValue("ref"))
	if err != nil {
		return err
	}
	w.Header().Set("ETag", etag(t.Version))
	ok(w, t)
	return nil
}

// ifMatchVersion parses an If-Match header carrying a task version ("3" or W/"3").
func ifMatchVersion(r *http.Request) (*int64, error) {
	h := strings.TrimSpace(r.Header.Get("If-Match"))
	if h == "" || h == "*" {
		return nil, nil
	}
	h = strings.Trim(strings.TrimPrefix(h, "W/"), `"`)
	v, err := strconv.ParseInt(h, 10, 64)
	if err != nil {
		return nil, badRequest("invalid_if_match", "If-Match must carry a task version, e.g. If-Match: \"3\"")
	}
	return &v, nil
}

func (a *API) updateTask(w http.ResponseWriter, r *http.Request, p service.Principal) error {
	var in service.UpdateTaskInput
	if err := decode(w, r, &in); err != nil {
		return err
	}
	v, err := ifMatchVersion(r)
	if err != nil {
		return err
	}
	if in.Version == nil {
		in.Version = v
	}
	t, err := a.svc.UpdateTask(r.Context(), p, r.PathValue("ref"), in)
	if err != nil {
		return err
	}
	w.Header().Set("ETag", etag(t.Version))
	ok(w, t)
	return nil
}

type moveRequest struct {
	Project string `json:"project"`
	Version *int64 `json:"version"`
}

func (a *API) moveTask(w http.ResponseWriter, r *http.Request, p service.Principal) error {
	var in moveRequest
	if err := decode(w, r, &in); err != nil {
		return err
	}
	if in.Project == "" {
		return &service.Error{Status: http.StatusUnprocessableEntity, Code: "validation_failed", Message: "project: is required",
			Fields: []service.FieldError{{Field: "project", Message: "is required"}}}
	}
	t, err := a.svc.UpdateTask(r.Context(), p, r.PathValue("ref"), service.UpdateTaskInput{Project: &in.Project, Version: in.Version})
	if err != nil {
		return err
	}
	w.Header().Set("ETag", etag(t.Version))
	ok(w, t)
	return nil
}

func (a *API) deleteTask(w http.ResponseWriter, r *http.Request, p service.Principal) error {
	v, err := ifMatchVersion(r)
	if err != nil {
		return err
	}
	if err := a.svc.DeleteTask(r.Context(), p, r.PathValue("ref"), v); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

var _ = time.Second

// --- preferences ----------------------------------------------------------------------

func (a *API) getPreferences(w http.ResponseWriter, r *http.Request, p service.Principal) error {
	prefs, err := a.svc.GetPreferences(r.Context(), p)
	if err != nil {
		return err
	}
	ok(w, prefs)
	return nil
}

// patchPreferences merges top-level keys into the caller's preferences; null deletes a key.
func (a *API) patchPreferences(w http.ResponseWriter, r *http.Request, p service.Principal) error {
	var patch map[string]json.RawMessage
	if err := decode(w, r, &patch); err != nil {
		return err
	}
	prefs, err := a.svc.MergePreferences(r.Context(), p, patch)
	if err != nil {
		return err
	}
	ok(w, prefs)
	return nil
}

// deleteProject permanently deletes an archived project; ?confirm= must repeat its key.
func (a *API) deleteProject(w http.ResponseWriter, r *http.Request, p service.Principal) error {
	if err := a.svc.DeleteProject(r.Context(), p, r.PathValue("ref"), r.URL.Query().Get("confirm")); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}
