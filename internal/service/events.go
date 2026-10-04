package service

import (
	"context"
	"sync"
)

// Event tells connected clients that something changed. It carries identifiers only, never
// content: clients re-fetch what they display, so access control is enforced by the normal
// read paths and an event can never leak task data.
type Event struct {
	Type      string `json:"type"` // task.created, task.updated, task.deleted, project.changed, resync
	ProjectID string `json:"project_id,omitempty"`
	TaskID    string `json:"task_id,omitempty"`
	Ref       string `json:"ref,omitempty"`
	// Origin is the X-LB-Client id of the tab that made the change, so it can ignore its own echo.
	Origin string `json:"origin,omitempty"`
}

// Subscription receives events for one principal.
type Subscription struct {
	userID  string
	allowed map[string]bool // optional token project allow-list
	C       chan Event
}

type hub struct {
	mu   sync.Mutex
	subs map[*Subscription]struct{}
}

type originKey struct{}

// WithOrigin tags a context with the originating client id (see Event.Origin).
func WithOrigin(ctx context.Context, origin string) context.Context {
	if origin == "" {
		return ctx
	}
	if len(origin) > 64 {
		origin = origin[:64]
	}
	return context.WithValue(ctx, originKey{}, origin)
}

func originFrom(ctx context.Context) string {
	o, _ := ctx.Value(originKey{}).(string)
	return o
}

// Subscribe registers for events visible to p. Call the returned function to unsubscribe.
func (s *Service) Subscribe(p Principal) (*Subscription, func()) {
	sub := &Subscription{userID: p.UserID, C: make(chan Event, 32)}
	if p.Token != nil && len(p.Token.ProjectIDs) > 0 {
		sub.allowed = map[string]bool{}
		for _, id := range p.Token.ProjectIDs {
			sub.allowed[id] = true
		}
	}
	s.hub.mu.Lock()
	if s.hub.subs == nil {
		s.hub.subs = map[*Subscription]struct{}{}
	}
	s.hub.subs[sub] = struct{}{}
	s.hub.mu.Unlock()
	return sub, func() {
		s.hub.mu.Lock()
		delete(s.hub.subs, sub)
		s.hub.mu.Unlock()
	}
}

func (sub *Subscription) send(ev Event) {
	select {
	case sub.C <- ev:
	default:
		// Slow consumer: drop the backlog and ask it to re-sync instead of blocking writers.
		for {
			select {
			case <-sub.C:
				continue
			default:
			}
			break
		}
		select {
		case sub.C <- Event{Type: "resync"}:
		default:
		}
	}
}

// publish notifies every subscriber who is a member of projectID.
func (s *Service) publish(ctx context.Context, projectID string, ev Event) {
	s.hub.mu.Lock()
	n := len(s.hub.subs)
	s.hub.mu.Unlock()
	if n == 0 {
		return
	}
	rows, err := s.db.R.QueryContext(ctx, `SELECT user_id FROM project_members WHERE project_id = ?`, projectID)
	if err != nil {
		return
	}
	members := map[string]bool{}
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			members[id] = true
		}
	}
	rows.Close()
	ev.ProjectID, ev.Origin = projectID, originFrom(ctx)
	s.deliver(ev, func(sub *Subscription) bool {
		return members[sub.userID] && (sub.allowed == nil || sub.allowed[projectID])
	})
}

// publishToUser sends an event to one user's connections regardless of membership (used when
// a user is added to or removed from a project).
func (s *Service) publishToUser(ctx context.Context, userID string, ev Event) {
	ev.Origin = originFrom(ctx)
	s.deliver(ev, func(sub *Subscription) bool { return sub.userID == userID })
}

func (s *Service) deliver(ev Event, match func(*Subscription) bool) {
	s.hub.mu.Lock()
	defer s.hub.mu.Unlock()
	for sub := range s.hub.subs {
		if match(sub) {
			sub.send(ev)
		}
	}
}
