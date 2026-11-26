package readiness

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/kakabisht/Docs-toolkit/internal/crawl"
	"github.com/kakabisht/Docs-toolkit/internal/model"
)

func site(sections map[string]int) *crawl.Site {
	s := &crawl.Site{Mode: crawl.Live}
	s.Pages = append(s.Pages, &model.Page{Key: "docs.example.test/", URL: "https://docs.example.test/", Source: "llms.txt", Status: 200})
	for sec, n := range sections {
		for i := 0; i < n; i++ {
			k := fmt.Sprintf("docs.example.test/%s/p%03d", sec, i)
			s.Pages = append(s.Pages, &model.Page{Key: k, URL: "https://" + k, Source: "sitemap", Status: 200})
		}
	}
	return s
}

func TestSampleIsStratifiedAndStable(t *testing.T) {
	s := site(map[string]int{"guide": 60, "reference": 30, "blog": 10})
	a := SamplePages(s, 20, 2000)
	b := SamplePages(s, 20, 2000)
	if fmt.Sprint(a.URLs) != fmt.Sprint(b.URLs) {
		t.Fatal("sample differs between runs")
	}
	if a.Size != 20 || a.Discovered != 101 || a.Mode != Indicative {
		t.Errorf("size %d discovered %d mode %s", a.Size, a.Discovered, a.Mode)
	}
	if a.URLs[0] != "https://docs.example.test/" {
		t.Errorf("entry page not sampled first: %v", a.URLs[:2])
	}
	if a.Sections["guide"] < a.Sections["reference"] || a.Sections["reference"] < a.Sections["blog"] || a.Sections["blog"] < 1 {
		t.Errorf("not proportional: %v", a.Sections)
	}
	if len(a.Warnings) == 0 {
		t.Error("a sample under 30 pages should warn")
	}
	all := SamplePages(s, 0, 2000)
	if all.Mode != Audit || all.Size != 101 {
		t.Errorf("full run: mode %s size %d", all.Mode, all.Size)
	}
	if trunc := SamplePages(s, 0, 101); trunc.Mode != Indicative || !trunc.Truncated {
		t.Errorf("hitting the page limit should be indicative: %+v", trunc.Mode)
	}
}

func loadAfdocs(t *testing.T, path string) *AfdocsResult {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var r AfdocsResult
	if err := json.Unmarshal(b, &r); err != nil {
		t.Fatal(err)
	}
	return &r
}

func TestCappedScoreIsNotHeadlined(t *testing.T) {
	af := loadAfdocs(t, filepath.Join("..", "..", "testdata", "fixtures", "agent-ready", "capped.json"))
	r := Combine("x", nil, af, "", 1)
	if r.Capped == nil || r.Capped.Cap != 59 || r.Capped.CheckID != "llms-txt-exists" {
		t.Fatalf("capped = %+v", r.Capped)
	}
	// 75 earned of 100 possible; the not-applicable check is left out
	if *r.AfdocsScore != 75 || r.Capped.Raw != 75 || r.Score != 75 {
		t.Errorf("afdocs %d raw %d score %d, want 75", *r.AfdocsScore, r.Capped.Raw, r.Score)
	}
}

func TestPreviewDropsHostChecks(t *testing.T) {
	var af AfdocsResult
	b, _ := os.ReadFile("../../testdata/fixtures/agent-ready/capped.json")
	if err := json.Unmarshal(b, &af); err != nil {
		t.Fatal(err)
	}
	left := af.Preview()
	if len(left) == 0 {
		t.Fatal("no host checks left out")
	}
	for _, r := range af.Results {
		if !RepoChecks[r.ID] && r.Status != "skip" {
			t.Errorf("%s still %s", r.ID, r.Status)
		}
	}
	if af.Scoring.Cap == nil || af.Scoring.Cap.CheckID != "llms-txt-exists" {
		t.Error("a cap from a repository check must stay")
	}
}
