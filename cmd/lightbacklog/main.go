// Command lightbacklog is the LightBacklog server and its admin CLI.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/superbyteone/lightbacklog/internal/api"
	"github.com/superbyteone/lightbacklog/internal/files"
	"github.com/superbyteone/lightbacklog/internal/mcp"
	"github.com/superbyteone/lightbacklog/internal/service"
	"github.com/superbyteone/lightbacklog/internal/store"
	"github.com/superbyteone/lightbacklog/web"
)

var version = "dev"

const usage = `usage: lightbacklog <command>

  serve                     run the web/API/MCP server (default)
  user create               create an account (--username, --admin; password from $LB_PASSWORD or stdin)
  user list                 list accounts
  user set-password         reset a password (--username; password from $LB_PASSWORD or stdin)
  token create              issue an API token (--user, --name, --scope read|write, --project KEY ..., --expires-days N)
  token list|revoke         list a user's tokens; revoke by --id or --name
  token rotate              replace a token with a fresh secret (--user, --id)
  mcp-audit list            show recent MCP calls (--token id, --limit N, default 100)
  backup [dir]              write a full backup archive (database + uploads), pruning old ones
  restore <archive> <dir>   extract and verify a backup into a new, empty directory
  healthcheck               probe the local server (used by the container health check)
  version
`

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envBool(key string, def bool) bool {
	if v := os.Getenv(key); v != "" {
		b, err := strconv.ParseBool(v)
		if err == nil {
			return b
		}
	}
	return def
}

func envInt(key string, def int64) int64 {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			return n
		}
	}
	return def
}

func main() {
	cmd := "serve"
	args := os.Args[1:]
	if len(args) > 0 {
		cmd, args = args[0], args[1:]
	}
	var err error
	switch cmd {
	case "serve":
		err = serve()
	case "user":
		err = userCmd(args)
	case "token":
		err = tokenCmd(args)
	case "mcp-audit":
		err = mcpAuditCmd(args)
	case "backup":
		err = backupCmd(args)
	case "restore":
		err = restoreCmd(args)
	case "healthcheck":
		err = healthcheck()
	case "version":
		fmt.Println(version)
	case "help", "-h", "--help":
		fmt.Print(usage)
	default:
		fmt.Fprint(os.Stderr, usage)
		err = fmt.Errorf("unknown command %q", cmd)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func dataDir() string { return env("LB_DATA_DIR", "./data") }

// open builds the service on top of the data directory (shared by serve and the CLI).
func open(ctx context.Context) (*store.DB, *service.Service, error) {
	db, err := store.Open(ctx, filepath.Join(dataDir(), "app.db"))
	if err != nil {
		return nil, nil, err
	}
	fs, err := files.NewStore(filepath.Join(dataDir(), "uploads"))
	if err != nil {
		db.Close()
		return nil, nil, err
	}
	maxUpload := envInt("LB_MAX_UPLOAD_MB", 10) << 20
	auditRetention := time.Duration(envInt("LB_MCP_AUDIT_RETENTION_DAYS", 90)) * 24 * time.Hour
	return db, service.New(db, fs, service.Config{MaxUploadBytes: maxUpload, MCPAuditRetention: auditRetention}), nil
}

// applyContainerMemoryLimit gives the Go runtime a soft limit below the container's hard cgroup
// limit, so the garbage collector works harder instead of the process being OOM-killed.
func applyContainerMemoryLimit(log *slog.Logger) {
	if os.Getenv("GOMEMLIMIT") != "" {
		return
	}
	for _, path := range []string{"/sys/fs/cgroup/memory.max", "/sys/fs/cgroup/memory/memory.limit_in_bytes"} {
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		limit, err := strconv.ParseInt(strings.TrimSpace(string(raw)), 10, 64)
		if err != nil || limit <= 0 || limit > 1<<40 {
			return // "max" or effectively unlimited
		}
		soft := limit / 100 * 70
		debug.SetMemoryLimit(soft)
		log.Info("memory limit detected", "cgroup_bytes", limit, "go_soft_limit_bytes", soft)
		return
	}
}

func serve() error {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	applyContainerMemoryLimit(log)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	db, svc, err := open(ctx)
	if err != nil {
		return err
	}
	defer db.Close()

	if n, err := svc.UserCount(ctx); err == nil && n == 0 {
		log.Warn("no accounts exist yet; create the first administrator with: lightbacklog user create --username <name> --admin")
	}

	go func() { // housekeeping: expired sessions, stale idempotency keys, orphaned uploads, old MCP audit rows
		t := time.NewTicker(time.Hour)
		defer t.Stop()
		for {
			if err := svc.PurgeExpired(ctx); err != nil && ctx.Err() == nil {
				log.Error("housekeeping failed", "err", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	}()

	audit := mcp.NewAuditSink(svc, log)
	auditDone := make(chan struct{})
	go func() { audit.Run(ctx); close(auditDone) }()

	maxUpload := envInt("LB_MAX_UPLOAD_MB", 10) << 20
	apiHandler := api.New(svc, api.Config{
		CookieSecure:   envBool("LB_COOKIE_SECURE", false),
		TrustProxy:     envBool("LB_TRUST_PROXY", true),
		Static:         web.Assets(),
		OpenAPI:        api.OpenAPISpec,
		Version:        version,
		MaxUploadBytes: maxUpload,
	}, log)

	mcpCIDRs, err := mcp.ParseAllowedCIDRs(env("LB_MCP_ALLOWED_CIDRS", ""))
	if err != nil {
		return err
	}
	mux := http.NewServeMux()
	mux.Handle("/mcp", mcp.Handler(svc, version, mcpCIDRs,
		float64(envInt("LB_MCP_RATE_RPS", 5)), int(envInt("LB_MCP_RATE_BURST", 20)), audit))
	mux.Handle("/", apiHandler)

	srv := &http.Server{
		Addr:              env("LB_ADDR", "127.0.0.1:8100"),
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       2 * time.Minute, // uploads
		WriteTimeout:      2 * time.Minute,
		IdleTimeout:       120 * time.Second,
	}
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	log.Info("listening", "addr", srv.Addr, "version", version)
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	<-auditDone // flush whatever MCP audit entries were still buffered
	return nil
}

// healthcheck probes the local /healthz endpoint; the distroless image has no curl.
func healthcheck() error {
	addr := env("LB_ADDR", "127.0.0.1:8100")
	if strings.HasPrefix(addr, "0.0.0.0") {
		addr = "127.0.0.1" + strings.TrimPrefix(addr, "0.0.0.0")
	}
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get("http://" + addr + "/healthz")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("healthz returned %d", resp.StatusCode)
	}
	return nil
}
