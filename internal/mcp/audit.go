package mcp

import (
	"context"
	"log/slog"
	"time"

	"github.com/superbyteone/lightbacklog/internal/service"
)

const (
	auditFlushInterval = 250 * time.Millisecond
	auditBatchSize     = 50
	auditBufferSize    = 1000
)

// AuditSink batches MCP audit entries and flushes them periodically in the background, so
// recording a call never adds write-lock latency to that call. If entries arrive faster than
// they can be flushed, the buffer drops the newest ones rather than blocking a tool call --
// audit logging must never cause an MCP outage.
type AuditSink struct {
	svc     *service.Service
	log     *slog.Logger
	entries chan service.MCPAuditEntry
}

func NewAuditSink(svc *service.Service, log *slog.Logger) *AuditSink {
	return &AuditSink{svc: svc, log: log, entries: make(chan service.MCPAuditEntry, auditBufferSize)}
}

// record queues one entry; it never blocks the caller.
func (a *AuditSink) record(e service.MCPAuditEntry) {
	select {
	case a.entries <- e:
	default:
		a.log.Warn("mcp audit buffer full; dropping entry", "tool", e.Tool)
	}
}

// Run flushes batches until ctx is done, then drains and flushes whatever remains before
// returning. Callers should wait for Run to return as part of shutdown.
func (a *AuditSink) Run(ctx context.Context) {
	t := time.NewTicker(auditFlushInterval)
	defer t.Stop()
	batch := make([]service.MCPAuditEntry, 0, auditBatchSize)
	flush := func() {
		if len(batch) == 0 {
			return
		}
		// A background write uses its own context: shutdown cancelling ctx must not abort
		// the final flush of whatever was already queued.
		if err := a.svc.RecordMCPAudit(context.Background(), batch); err != nil {
			a.log.Error("mcp audit flush failed", "err", err)
		}
		batch = batch[:0]
	}
	for {
		select {
		case e := <-a.entries:
			batch = append(batch, e)
			if len(batch) >= auditBatchSize {
				flush()
			}
		case <-t.C:
			flush()
		case <-ctx.Done():
			for {
				select {
				case e := <-a.entries:
					batch = append(batch, e)
				default:
					flush()
					return
				}
			}
		}
	}
}
