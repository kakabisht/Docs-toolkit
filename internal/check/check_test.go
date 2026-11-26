package check

import (
	"context"
	"encoding/json"
	"flag"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kakabisht/Docs-toolkit/internal/crawl"
	"github.com/kakabisht/Docs-toolkit/internal/fetch"
	"github.com/kakabisht/Docs-toolkit/internal/finding"
	"github.com/kakabisht/Docs-toolkit/internal/vocab"
)

var update = flag.Bool("update", false, "rewrite golden files")

func fixture(name string) string { return filepath.Join("..", "..", "testdata", "fixtures", name) }

func runFixture(t *testing.T, dir string) []finding.Finding {
	t.Helper()
	s, err := crawl.LoadLocal(dir, crawl.LocalOptions{NoGit: true})
	if err != nil {
		t.Fatal(err)
	}
	opt := Options{Offline: true}
	if _, err := os.Stat(filepath.Join(dir, "frontmatter.schema.yaml")); err == nil {
		if opt.Schema, err = CompileSchema(filepath.Join(dir, "frontmatter.schema.yaml")); err != nil {
			t.Fatal(err)
		}
	}
	if opt.Vocab, err = vocab.Load(filepath.Join(dir, "vocab.yaml")); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return Run(context.Background(), s, opt)
}

// TestBrokenFixture compares findings on a docs set with one deliberate
// problem per rule against a golden file, and checks runs are identical.
func TestBrokenFixture(t *testing.T) {
	dir := fixture("broken")
	a := runFixture(t, dir)
	b := runFixture(t, dir)
	ja, _ := json.MarshalIndent(a, "", "  ")
	jb, _ := json.MarshalIndent(b, "", "  ")
	if string(ja) != string(jb) {
		t.Fatal("two runs gave different findings")
	}
	golden := filepath.Join(dir, "expected.json")
	if *update {
		os.WriteFile(golden, append(ja, '\n'), 0o644)
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(want)) != strings.TrimSpace(string(ja)) {
		t.Errorf("findings differ from %s (run with -update to accept):\n%s", golden, ja)
	}
	// every offline rule is exercised
	seen := map[string]bool{}
	for _, f := range a {
		seen[f.Rule] = true
	}
	for _, id := range finding.RuleIDs() {
		if strings.HasPrefix(id, "link-external-") || id == "draft-unverified-claims" {
			continue // covered by TestExternal and the draft tests
		}
		if !seen[id] {
			t.Errorf("rule %s not exercised by the broken fixture", id)
		}
	}
}

func TestCleanFixture(t *testing.T) {
	if fs := runFixture(t, fixture("clean")); len(fs) != 0 {
		t.Errorf("clean fixture has findings: %+v", fs)
	}
}

func TestExternal(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/ok", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<html><body><h2 id="present">x</h2></body></html>`))
	})
	mux.HandleFunc("/gone", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(404) })
	mux.HandleFunc("/deleted", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(410) })
	mux.HandleFunc("/error", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) })
	mux.HandleFunc("/moved", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/ok", 301) })
	mux.HandleFunc("/temp", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/ok", 302) })
	mux.HandleFunc("/bot", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(403) })
	mux.HandleFunc("/busy", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(429) })
	mux.HandleFunc("/slow", func(w http.ResponseWriter, r *http.Request) { time.Sleep(600 * time.Millisecond) })
	srv := httptest.NewServer(mux)
	defer srv.Close()

	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "docs"), 0o755)
	var body strings.Builder
	body.WriteString("---\ntitle: Links\n---\n\n")
	for _, p := range []string{"ok", "ok#present", "ok#absent", "gone", "deleted", "error", "moved", "temp", "bot", "busy", "slow"} {
		body.WriteString("- [" + p + "](" + srv.URL + "/" + p + ")\n")
	}
	body.WriteString("- [placeholder](https://example.com/docs)\n")
	os.WriteFile(filepath.Join(dir, "docs", "index.md"), []byte(body.String()), 0o644)

	s, err := crawl.LoadLocal(dir, crawl.LocalOptions{NoGit: true})
	if err != nil {
		t.Fatal(err)
	}
	client := fetch.New(fetch.Options{Timeout: 300 * time.Millisecond, Retries: 1, Backoff: 10 * time.Millisecond})
	fs := Run(context.Background(), s, Options{Client: client, ExternalAnchor: true, CheckLocalhost: true})
	got := map[string]string{}
	for _, f := range fs {
		got[strings.TrimPrefix(f.Target, srv.URL+"/")] = f.Rule
	}
	want := map[string]string{
		"ok#absent": "link-external-anchor",
		"gone":      "link-external-broken",
		"deleted":   "link-external-broken",
		"error":     "link-external-server-error",
		"moved":     "link-external-redirect",
		"bot":       "link-external-unverified",
		"busy":      "link-external-unverified",
		"slow":      "link-external-timeout",
	}
	for k, rule := range want {
		if got[k] != rule {
			t.Errorf("%s: got %q, want %q", k, got[k], rule)
		}
	}
	for _, k := range []string{"ok", "ok#present", "temp", "http://localhost:3000/x", "https://example.com/docs"} {
		if r, ok := got[k]; ok {
			t.Errorf("%s reported as %s", k, r)
		}
	}
	// offline skips them all
	if fs := Run(context.Background(), s, Options{Client: client, Offline: true}); len(fs) != 0 {
		t.Errorf("offline findings: %+v", fs)
	}
}

func TestFilterAndPolicy(t *testing.T) {
	s, err := crawl.LoadLocal(fixture("broken"), crawl.LocalOptions{NoGit: true})
	if err != nil {
		t.Fatal(err)
	}
	fs := Run(context.Background(), s, Options{Offline: true, Filter: map[string]bool{"docs/index.md": true},
		Policy: finding.Policy{Overrides: map[string]finding.Severity{"link-empty": finding.Off}, Only: map[finding.Category]bool{finding.Links: true}}})
	if len(fs) == 0 {
		t.Fatal("no findings")
	}
	for _, f := range fs {
		if f.File != "docs/index.md" || f.Rule == "link-empty" || finding.Rules[f.Rule].Category != finding.Links {
			t.Errorf("unexpected %+v", f)
		}
	}
}

func TestSingleFile(t *testing.T) {
	s, err := crawl.LoadLocal(filepath.Join(fixture("broken"), "docs", "ref", "cli.md"), crawl.LocalOptions{NoGit: true})
	if err != nil {
		t.Fatal(err)
	}
	fs := Run(context.Background(), s, Options{Offline: true})
	if len(fs) == 0 {
		t.Fatal("no findings for cli.md")
	}
	for _, f := range fs {
		if f.File != "docs/ref/cli.md" {
			t.Errorf("finding outside the file: %+v", f)
		}
	}
}
