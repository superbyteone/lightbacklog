// Package files stores uploaded attachments on local disk, content-addressed by SHA-256,
// and decides which content types may be accepted and whether they may be shown inline.
package files

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

var (
	ErrTooLarge    = errors.New("file too large")
	ErrUnsupported = errors.New("unsupported file type")
)

// Store keeps blobs under root as <root>/<aa>/<sha256>.
type Store struct{ root string }

// NewStore creates the storage directory (owner-only) if needed.
func NewStore(root string) (*Store, error) {
	if err := os.MkdirAll(filepath.Join(root, "tmp"), 0o700); err != nil {
		return nil, err
	}
	return &Store{root: root}, nil
}

// Staged is an upload that has been fully received and hashed but not yet committed.
type Staged struct {
	SHA256 string
	Size   int64
	Head   []byte // first bytes, for content sniffing
	tmp    string
	store  *Store
}

// Stage receives up to max bytes into a temporary file, hashing as it goes.
func (s *Store) Stage(r io.Reader, max int64) (*Staged, error) {
	f, err := os.CreateTemp(filepath.Join(s.root, "tmp"), "up-*")
	if err != nil {
		return nil, err
	}
	name := f.Name()
	fail := func(e error) (*Staged, error) {
		f.Close()
		os.Remove(name)
		return nil, e
	}
	h := sha256.New()
	head := make([]byte, 0, 512)
	n, err := io.Copy(io.MultiWriter(f, h, &headWriter{buf: &head}), io.LimitReader(r, max+1))
	if err != nil {
		return fail(err)
	}
	if n > max {
		return fail(ErrTooLarge)
	}
	if err := f.Close(); err != nil {
		os.Remove(name)
		return nil, err
	}
	return &Staged{SHA256: hex.EncodeToString(h.Sum(nil)), Size: n, Head: head, tmp: name, store: s}, nil
}

type headWriter struct{ buf *[]byte }

func (w *headWriter) Write(p []byte) (int, error) {
	if room := 512 - len(*w.buf); room > 0 {
		if len(p) < room {
			room = len(p)
		}
		*w.buf = append(*w.buf, p[:room]...)
	}
	return len(p), nil
}

func (s *Store) path(sha string) string { return filepath.Join(s.root, sha[:2], sha) }

// Commit moves the staged file into place (a no-op if identical content already exists).
func (st *Staged) Commit() error {
	dst := st.store.path(st.SHA256)
	if _, err := os.Stat(dst); err == nil {
		return os.Remove(st.tmp)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	if err := os.Chmod(st.tmp, 0o600); err != nil {
		return err
	}
	return os.Rename(st.tmp, dst)
}

// Discard drops an uncommitted upload.
func (st *Staged) Discard() { os.Remove(st.tmp) }

// Open opens a stored blob for reading.
func (s *Store) Open(sha string) (*os.File, error) {
	if len(sha) != 64 {
		return nil, os.ErrNotExist
	}
	return os.Open(s.path(sha))
}

// Remove deletes a blob; callers must ensure no attachment row still references it.
func (s *Store) Remove(sha string) error {
	if len(sha) != 64 {
		return nil
	}
	err := os.Remove(s.path(sha))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// Kind describes how a validated upload may be served.
type Kind struct {
	MIME   string
	Inline bool // safe to render in the browser (raster images only)
}

// Detect classifies content by its bytes (never by the client-supplied name or type).
// Only raster images (inline), PDFs, zip/gzip archives and plain text (download only)
// are accepted. HTML, XML and SVG are rejected outright.
func Detect(head []byte, filename string) (Kind, error) {
	ct := http.DetectContentType(head)
	base, _, _ := strings.Cut(ct, ";")
	switch base {
	case "image/png", "image/jpeg", "image/gif", "image/webp":
		return Kind{MIME: base, Inline: true}, nil
	case "application/pdf", "application/zip":
		return Kind{MIME: base}, nil
	case "application/x-gzip":
		return Kind{MIME: "application/gzip"}, nil
	case "text/plain":
		if looksLikeMarkup(head) {
			return Kind{}, fmt.Errorf("%w: markup files (HTML/SVG/XML) are not accepted", ErrUnsupported)
		}
		return Kind{MIME: "text/plain; charset=utf-8"}, nil
	}
	return Kind{}, fmt.Errorf("%w: %s", ErrUnsupported, base)
}

// looksLikeMarkup catches SVG/HTML that slipped past sniffing as plain text.
func looksLikeMarkup(head []byte) bool {
	s := strings.ToLower(strings.TrimSpace(string(head)))
	for _, p := range []string{"<svg", "<html", "<!doctype", "<?xml", "<script", "<body"} {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}
