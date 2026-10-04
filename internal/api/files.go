package api

import (
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strings"

	"github.com/superbyteone/lightbacklog/internal/service"
)

type attachmentResponse struct {
	*service.Attachment
	// Markdown is a ready-to-paste snippet for a task description.
	Markdown string `json:"markdown"`
}

func markdownFor(a *service.Attachment) string {
	name := strings.NewReplacer("[", "(", "]", ")").Replace(a.Filename)
	if a.Inline {
		return "![" + name + "](" + a.URL + ")"
	}
	return "[" + name + "](" + a.URL + ")"
}

func (a *API) uploadToTask(w http.ResponseWriter, r *http.Request, p service.Principal) error {
	return a.upload(w, r, p, r.PathValue("ref"))
}

func (a *API) uploadToProject(w http.ResponseWriter, r *http.Request, p service.Principal) error {
	return a.upload(w, r, p, "")
}

// upload accepts multipart/form-data (field "file", plus optional "project"/"task" fields
// placed before it) or a raw body with ?filename=. project/task may also be query parameters.
func (a *API) upload(w http.ResponseWriter, r *http.Request, p service.Principal, taskRef string) error {
	project := r.URL.Query().Get("project")
	if taskRef == "" {
		taskRef = r.URL.Query().Get("task")
	}
	limit := a.cfg.MaxUploadBytes
	if limit <= 0 {
		limit = 10 << 20
	}
	r.Body = http.MaxBytesReader(w, r.Body, limit+(1<<20))

	var att *service.Attachment
	mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if mt == "multipart/form-data" {
		mr, err := r.MultipartReader()
		if err != nil {
			return badRequest("invalid_multipart", "invalid multipart body: "+err.Error())
		}
		for {
			part, err := mr.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				return err
			}
			switch part.FormName() {
			case "project":
				project = readField(part)
			case "task":
				taskRef = readField(part)
			case "file":
				att, err = a.svc.Upload(r.Context(), p, project, taskRef, part.FileName(), part)
				if err != nil {
					return err
				}
			}
			if att != nil {
				break
			}
		}
		if att == nil {
			return &service.Error{Status: http.StatusUnprocessableEntity, Code: "validation_failed", Message: "file: a multipart part named 'file' is required (send project/task fields before it)",
				Fields: []service.FieldError{{Field: "file", Message: "is required"}}}
		}
	} else {
		name := r.URL.Query().Get("filename")
		if name == "" {
			name = r.Header.Get("X-Filename")
		}
		var err error
		if att, err = a.svc.Upload(r.Context(), p, project, taskRef, name, r.Body); err != nil {
			return err
		}
	}
	created(w, attachmentResponse{Attachment: att, Markdown: markdownFor(att)})
	return nil
}

func readField(part *multipart.Part) string {
	b, _ := io.ReadAll(io.LimitReader(part, 512))
	return strings.TrimSpace(string(b))
}

func (a *API) listAttachments(w http.ResponseWriter, r *http.Request, p service.Principal) error {
	list, err := a.svc.ListAttachments(r.Context(), p, r.PathValue("ref"))
	if err != nil {
		return err
	}
	out := make([]attachmentResponse, len(list))
	for i := range list {
		out[i] = attachmentResponse{Attachment: &list[i], Markdown: markdownFor(&list[i])}
	}
	ok(w, out)
	return nil
}

func (a *API) deleteAttachment(w http.ResponseWriter, r *http.Request, p service.Principal) error {
	if err := a.svc.DeleteAttachment(r.Context(), p, r.PathValue("id")); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// serveFile streams an attachment after checking project membership. Only raster images
// are shown inline; everything else downloads. The sandbox CSP and nosniff make even a
// hostile file inert if it is ever opened directly.
func (a *API) serveFile(w http.ResponseWriter, r *http.Request, p service.Principal) error {
	att, f, err := a.svc.OpenAttachment(r.Context(), p, r.PathValue("id"))
	if err != nil {
		return err
	}
	defer f.Close()
	h := w.Header()
	h.Set("Content-Type", att.MIME)
	disp := "attachment"
	if att.Inline {
		disp = "inline"
	}
	h.Set("Content-Disposition", mime.FormatMediaType(disp, map[string]string{"filename": att.Filename}))
	h.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	h.Set("Cache-Control", "private, max-age=300")
	http.ServeContent(w, r, att.Filename, att.CreatedAt, f)
	return nil
}
