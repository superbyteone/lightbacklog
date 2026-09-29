package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/superbyteone/lightbacklog/internal/service"
)

// readPassword takes the password from $LB_PASSWORD or the first line of stdin, never from argv
// (which would be visible in the process list and shell history).
func readPassword() (string, error) {
	if v := os.Getenv("LB_PASSWORD"); v != "" {
		return v, nil
	}
	fmt.Fprintln(os.Stderr, "reading password from stdin (one line)...")
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && line == "" {
		return "", fmt.Errorf("no password provided: set LB_PASSWORD or pipe it on stdin")
	}
	return strings.TrimRight(line, "\r\n"), nil
}

func userCmd(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("user: expected create, list or set-password")
	}
	sub, rest := args[0], args[1:]
	fs := flag.NewFlagSet("user "+sub, flag.ContinueOnError)
	username := fs.String("username", "", "account name")
	email := fs.String("email", "", "email address (optional)")
	admin := fs.Bool("admin", false, "grant administrator rights")
	if err := fs.Parse(rest); err != nil {
		return err
	}
	ctx := context.Background()
	db, svc, err := open(ctx)
	if err != nil {
		return err
	}
	defer db.Close()
	switch sub {
	case "create":
		if *username == "" {
			return fmt.Errorf("--username is required")
		}
		pw, err := readPassword()
		if err != nil {
			return err
		}
		n, _ := svc.UserCount(ctx)
		u, err := svc.CreateUser(ctx, service.CreateUserInput{Username: *username, Email: *email, Password: pw, IsAdmin: *admin || n == 0})
		if err != nil {
			return err
		}
		fmt.Printf("created user %s (id %s, admin=%v)\n", u.Username, u.ID, u.IsAdmin)
	case "list":
		// The CLI has full local access; reuse the admin listing via an admin principal.
		users, err := svc.ListUsers(ctx, service.Principal{IsAdmin: true})
		if err != nil {
			return err
		}
		for _, u := range users {
			state := ""
			if u.DisabledAt != nil {
				state = " (disabled)"
			}
			fmt.Printf("%-24s admin=%-5v id=%s%s\n", u.Username, u.IsAdmin, u.ID, state)
		}
	case "set-password":
		if *username == "" {
			return fmt.Errorf("--username is required")
		}
		pw, err := readPassword()
		if err != nil {
			return err
		}
		if err := svc.SetPassword(ctx, *username, pw); err != nil {
			return err
		}
		fmt.Println("password updated; existing sessions were revoked")
	default:
		return fmt.Errorf("user: unknown subcommand %q", sub)
	}
	return nil
}

type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }

func tokenCmd(args []string) error {
	if len(args) == 0 || (args[0] != "create" && args[0] != "list" && args[0] != "revoke" && args[0] != "rotate") {
		return fmt.Errorf("token: expected create, list, revoke or rotate")
	}
	if args[0] == "rotate" {
		return tokenRotate(args[1:])
	}
	if args[0] != "create" {
		return tokenAdmin(args[0], args[1:])
	}
	fs := flag.NewFlagSet("token create", flag.ContinueOnError)
	user := fs.String("user", "", "owner account")
	name := fs.String("name", "", "token label, e.g. my-agent")
	scope := fs.String("scope", "write", "read or write")
	expiresDays := fs.Int("expires-days", 0, "expire the token after this many days (0, the default, means never)")
	var projects multiFlag
	fs.Var(&projects, "project", "restrict to this project key or id (repeatable)")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if *user == "" || *name == "" {
		return fmt.Errorf("--user and --name are required")
	}
	ctx := context.Background()
	db, svc, err := open(ctx)
	if err != nil {
		return err
	}
	defer db.Close()
	p, err := svc.PrincipalFor(ctx, *user)
	if err != nil {
		return err
	}
	tok, secret, err := svc.CreateToken(ctx, p, service.CreateTokenInput{Name: *name, Scope: *scope, Projects: projects, ExpiresInDays: *expiresDays})
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "token %s (%s, scope %s) created; the secret is shown once:\n", tok.Name, tok.Prefix, tok.Scope)
	fmt.Println(secret)
	return nil
}

// tokenRotate replaces a token by id with a fresh secret, atomically, keeping its name, scope,
// project restriction and expiry policy.
func tokenRotate(args []string) error {
	fs := flag.NewFlagSet("token rotate", flag.ContinueOnError)
	user := fs.String("user", "", "owner account")
	id := fs.String("id", "", "token id to rotate")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *user == "" || *id == "" {
		return fmt.Errorf("--user and --id are required")
	}
	ctx := context.Background()
	db, svc, err := open(ctx)
	if err != nil {
		return err
	}
	defer db.Close()
	p, err := svc.PrincipalFor(ctx, *user)
	if err != nil {
		return err
	}
	tok, secret, err := svc.RotateToken(ctx, p, *id)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "token %s (%s, scope %s) rotated; the new secret is shown once:\n", tok.Name, tok.Prefix, tok.Scope)
	fmt.Println(secret)
	return nil
}

func mkdirPrivate(dir string) error { return os.MkdirAll(dir, 0o700) }

// mcpAuditCmd shows recent MCP calls recorded in mcp_audit_log -- forensic lookup only, there
// is no REST/UI surface for this in this release.
func mcpAuditCmd(args []string) error {
	if len(args) == 0 || args[0] != "list" {
		return fmt.Errorf("mcp-audit: expected list")
	}
	fs := flag.NewFlagSet("mcp-audit list", flag.ContinueOnError)
	token := fs.String("token", "", "restrict to this token id")
	limit := fs.Int("limit", 100, "maximum rows to show (most recent first)")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	ctx := context.Background()
	db, svc, err := open(ctx)
	if err != nil {
		return err
	}
	defer db.Close()
	rows, err := svc.ListMCPAudit(ctx, *token, *limit)
	if err != nil {
		return err
	}
	for _, r := range rows {
		errCode := "-"
		if r.ErrorCode != "" {
			errCode = r.ErrorCode
		}
		fmt.Printf("%s  token=%-38s user=%-38s %-20s %-5s %-20s %5dms  %s\n",
			r.CreatedAt.Format("2006-01-02 15:04:05"), orDash(r.TokenID), orDash(r.UserID), r.Tool, r.Status, errCode, r.DurationMS, r.ClientIP)
	}
	return nil
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// tokenAdmin lists a user's API tokens or revokes them by id or name.
func tokenAdmin(sub string, args []string) error {
	fs := flag.NewFlagSet("token "+sub, flag.ContinueOnError)
	user := fs.String("user", "", "owner account")
	id := fs.String("id", "", "token id (revoke)")
	name := fs.String("name", "", "token name (revoke; revokes every active token with this name)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *user == "" {
		return fmt.Errorf("--user is required")
	}
	ctx := context.Background()
	db, svc, err := open(ctx)
	if err != nil {
		return err
	}
	defer db.Close()
	p, err := svc.PrincipalFor(ctx, *user)
	if err != nil {
		return err
	}
	tokens, err := svc.ListTokens(ctx, p)
	if err != nil {
		return err
	}
	if sub == "list" {
		for _, t := range tokens {
			state := "active"
			if t.RevokedAt != nil {
				state = "revoked"
			}
			used := "never used"
			if t.LastUsedAt != nil {
				used = "last used " + t.LastUsedAt.Format("2006-01-02 15:04")
			}
			expiry := "never expires"
			if t.ExpiresAt != nil {
				expiry = "expires " + t.ExpiresAt.Format("2006-01-02")
			}
			fmt.Printf("%s  %-28s %s…  %-5s  %-7s  %-14s  %s\n", t.ID, t.Name, t.Prefix, t.Scope, state, expiry, used)
		}
		return nil
	}
	if *id == "" && *name == "" {
		return fmt.Errorf("--id or --name is required")
	}
	n := 0
	for _, t := range tokens {
		if t.RevokedAt == nil && (t.ID == *id || (*name != "" && t.Name == *name)) {
			if err := svc.RevokeToken(ctx, p, t.ID); err != nil {
				return err
			}
			n++
		}
	}
	if n == 0 {
		return fmt.Errorf("no matching active token")
	}
	fmt.Printf("revoked %d token(s)\n", n)
	return nil
}
