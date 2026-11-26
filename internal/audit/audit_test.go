package audit

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kakabisht/Docs-toolkit/internal/crawl"
	"github.com/kakabisht/Docs-toolkit/internal/vocab"
)

var update = flag.Bool("update", false, "rewrite golden files")

const fixture = "../../testdata/fixtures/graph"

func run(t *testing.T) *Report {
	t.Helper()
	s, err := crawl.LoadLocal(fixture, crawl.LocalOptions{NoGit: true})
	if err != nil {
		t.Fatal(err)
	}
	v, err := vocab.Load(filepath.Join(fixture, "vocab.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	tasks, err := LoadTasks(filepath.Join(fixture, "tasks.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	qs, err := LoadQueryLog(filepath.Join(fixture, "queries.csv"))
	if err != nil {
		t.Fatal(err)
	}
	r := Run(s, Options{Vocab: v, Tasks: tasks, Queries: qs, Now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)})
	r.Source = "testdata/fixtures/graph"
	return r
}

func TestGoldenReport(t *testing.T) {
	r := run(t)
	var md bytes.Buffer
	r.Markdown(&md, 0)
	golden := filepath.Join(fixture, "expected-audit.md")
	if *update {
		os.WriteFile(golden, md.Bytes(), 0o644)
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if md.String() != string(want) {
		t.Errorf("report differs from %s; run go test ./internal/audit -update and review the diff", golden)
	}
	var a, b bytes.Buffer
	json.NewEncoder(&a).Encode(r)
	json.NewEncoder(&b).Encode(run(t))
	if a.String() != b.String() {
		t.Error("two runs differ")
	}
}

func TestTasksAndQueries(t *testing.T) {
	r := run(t)
	found := map[string]bool{}
	for _, tr := range r.Tasks {
		found[tr.Task] = tr.Found
	}
	for task, want := range map[string]bool{"Install lumen": true, "Change the server port": true, "Fix a refused connection": true, "Uninstall lumen": false} {
		if found[task] != want {
			t.Errorf("task %q found = %v, want %v", task, found[task], want)
		}
	}
	gaps := map[string]bool{}
	for _, g := range r.QueryGaps {
		gaps[g.Query] = true
	}
	if !gaps["uninstall lumen"] || gaps["install"] || gaps["port"] {
		t.Errorf("query gaps = %v", r.QueryGaps)
	}
}

func TestScoresAreTransparent(t *testing.T) {
	r := run(t)
	if r.Score <= 0 || r.Score > 100 {
		t.Fatalf("score %v", r.Score)
	}
	for _, d := range r.Dimensions {
		if d.Method == "" || d.Summary == "" {
			t.Errorf("%s has no method or summary", d.ID)
		}
		if d.Scored && (d.Score < 0 || d.Score > 100) {
			t.Errorf("%s score %v", d.ID, d.Score)
		}
		if _, ok := r.Weights[d.ID]; !ok {
			t.Errorf("%s has no weight", d.ID)
		}
	}
	if len(r.FixFirst) == 0 || len(r.FixFirst) > 10 {
		t.Errorf("fix first has %d items", len(r.FixFirst))
	}
	for i := 1; i < len(r.FixFirst); i++ {
		if r.FixFirst[i].Priority > r.FixFirst[i-1].Priority {
			t.Error("fix first is not sorted by priority")
		}
	}
}

func TestQueryLogFormats(t *testing.T) {
	for in, want := range map[string]int{
		"query,count\nfoo,3\nbar,2\nfoo,1\n":         4,
		"foo,3\nbar,2\n":                             3,
		"Search term,Searches\n\"a, b\",\"1,200\"\n": 1200,
		"foo\nbar\n":                                 1,
	} {
		qs, err := readQueryLog(strings.NewReader(in))
		if err != nil {
			t.Fatal(err)
		}
		if len(qs) == 0 || qs[0].Count != want {
			t.Errorf("%q: %+v, want first count %d", in, qs, want)
		}
	}
}
