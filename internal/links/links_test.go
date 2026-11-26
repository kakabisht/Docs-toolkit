package links_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/docsbot/docsbot/internal/crawl"
	"github.com/docsbot/docsbot/internal/devserver"
	"github.com/docsbot/docsbot/internal/fetch"
	"github.com/docsbot/docsbot/internal/graph"
	"github.com/docsbot/docsbot/internal/links"
)

// externalServer stands in for other websites.
func externalServer(t *testing.T) *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/ok", func(w http.ResponseWriter, r *http.Request) {})
	mux.HandleFunc("/moved", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/ok", http.StatusMovedPermanently) })
	mux.HandleFunc("/blocked", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusForbidden) })
	mux.HandleFunc("/headless", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// fixture copies testdata/site to a temp dir with https://ext.invalid pointing at ext.
func fixture(t *testing.T, ext string) string {
	t.Helper()
	src := filepath.Join("..", "..", "testdata", "site")
	dst := t.TempDir()
	err := filepath.Walk(src, func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		out := filepath.Join(dst, rel)
		if fi.IsDir() {
			return os.MkdirAll(out, 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(out, []byte(strings.ReplaceAll(string(b), "https://ext.invalid", ext)), 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	return dst
}

func client() *fetch.Client { return fetch.New(fetch.Options{Concurrency: 4}) }

func summary(rep *links.Report) []string {
	var out []string
	for _, is := range rep.Issues {
		out = append(out, is.Problem+" "+is.Kind+" "+is.Target)
	}
	sort.Strings(out)
	return out
}

func TestLocal(t *testing.T) {
	ext := externalServer(t)
	dir := fixture(t, ext.URL)
	s, err := crawl.LoadLocal(dir, crawl.LocalOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if s.Content != "docs" || len(s.Pages) != 7 || !s.LLMSTxt.Found {
		t.Fatalf("content=%s pages=%d llms=%v", s.Content, len(s.Pages), s.LLMSTxt.Found)
	}
	g := graph.Build(s)
	rep := links.Check(context.Background(), g, client(), links.Options{External: true, Redirects: true})
	got := summary(rep)
	want := []string{
		"blocked external " + ext.URL + "/blocked",
		"broken asset /img/gone.png",
		"broken external " + ext.URL + "/gone",
		"broken internal /nope",
		"broken-anchor anchor /guide/install#does-not-exist",
		"broken-anchor internal /guide/install#nope",
		"redirect external " + ext.URL + "/moved",
		"redirect internal /quickstart/",
	}
	sort.Strings(want)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("issues:\n%s\n\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if !rep.Failed([]string{"broken"}) || rep.Failed([]string{"unreachable"}) {
		t.Error("Failed() mismatch")
	}
	// the orphan page in a folder with no index
	st := g.ComputeStats(5)
	if len(st.Orphans) != 1 || st.Orphans[0] != "/misc/orphan" {
		t.Errorf("orphans: %v", st.Orphans)
	}
}

func TestLive(t *testing.T) {
	ext := externalServer(t)
	dir := fixture(t, ext.URL)
	ds, err := devserver.New(dir, crawl.LocalOptions{})
	if err != nil {
		t.Fatal(err)
	}
	site := httptest.NewServer(ds)
	defer site.Close()

	c := client()
	s, err := crawl.LoadLive(context.Background(), c, site.URL+"/", crawl.LiveOptions{Follow: true})
	if err != nil {
		t.Fatal(err)
	}
	g := graph.Build(s)
	st := g.ComputeStats(5)
	if st.Pages != 7 || !g.LLMSTxt.Found {
		t.Fatalf("pages=%d (errors %d) llms=%v", st.Pages, st.ErrorPages, g.LLMSTxt.Found)
	}
	rep := links.Check(context.Background(), g, c, links.Options{External: true})
	got := strings.Join(summary(rep), "\n")
	for _, want := range []string{
		"broken internal " + site.URL + "/nope/",
		"broken asset " + site.URL + "/img/gone.png",
		"broken external " + ext.URL + "/gone",
		"broken-anchor anchor " + site.URL + "/guide/install/#does-not-exist",
		"blocked external " + ext.URL + "/blocked",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, ext.URL+"/ok") {
		t.Errorf("ok URL reported:\n%s", got)
	}
}
