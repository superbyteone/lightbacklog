package api

import (
	"regexp"
	"sort"
	"strings"
	"testing"
)

// TestOpenAPIDocumentsEveryRoute keeps the hand-written spec honest: each registered route must
// appear in openapi.yaml and vice versa.
func TestOpenAPIDocumentsEveryRoute(t *testing.T) {
	pathRe := regexp.MustCompile(`^  (/[^:\s]*):\s*$`)
	methodRe := regexp.MustCompile(`^    (get|post|put|patch|delete):\s*$`)
	documented := map[string]bool{}
	cur := ""
	for _, line := range strings.Split(string(OpenAPISpec), "\n") {
		if strings.HasPrefix(line, "components:") {
			break
		}
		if m := pathRe.FindStringSubmatch(line); m != nil {
			cur = m[1]
		} else if m := methodRe.FindStringSubmatch(line); m != nil && cur != "" {
			documented[strings.ToUpper(m[1])+" "+cur] = true
		}
	}
	h := newHarness(t)
	_ = h
	a := New(h.svc, Config{}, nil)
	registered := map[string]bool{}
	for _, r := range a.Routes() {
		registered[r] = true
	}
	var missing, extra []string
	for r := range registered {
		if !documented[r] {
			missing = append(missing, r)
		}
	}
	for d := range documented {
		if !registered[d] {
			extra = append(extra, d)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	if len(missing) > 0 {
		t.Errorf("routes missing from openapi.yaml:\n  %s", strings.Join(missing, "\n  "))
	}
	if len(extra) > 0 {
		t.Errorf("openapi.yaml documents routes that do not exist:\n  %s", strings.Join(extra, "\n  "))
	}
	if len(documented) < 35 {
		t.Errorf("suspiciously few documented operations: %d", len(documented))
	}
}
