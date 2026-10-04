package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/superbyteone/lightbacklog/internal/service"
)

// events streams change notifications (Server-Sent Events) so open browsers update without
// polling. Events contain identifiers only; clients re-fetch through the normal, access-checked
// endpoints.
func (a *API) events(w http.ResponseWriter, r *http.Request, p service.Principal) error {
	flusher, ok := w.(http.Flusher)
	if !ok {
		return &service.Error{Status: http.StatusInternalServerError, Code: "internal", Message: "streaming is not supported"}
	}
	// The server-wide write timeout must not cut a long-lived stream.
	_ = http.NewResponseController(w).SetWriteDeadline(time.Time{})
	sub, cancel := a.svc.Subscribe(p)
	defer cancel()

	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache, no-transform")
	h.Set("X-Accel-Buffering", "no") // tell Nginx not to buffer
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, "retry: 3000\n: connected\n\n")
	flusher.Flush()

	beat := time.NewTicker(25 * time.Second)
	defer beat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return nil
		case <-beat.C:
			fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		case ev := <-sub.C:
			data, _ := json.Marshal(ev)
			if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Type, data); err != nil {
				return nil
			}
			flusher.Flush()
		}
	}
}
