package draft

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
)

var update = flag.Bool("update", false, "rewrite golden files")

const (
	kb = "../../testdata/fixtures/graph/docs"
	tx = "../../testdata/fixtures/draft"
)

func transcripts() []string {
	return []string{tx + "/call.vtt", tx + "/interview.srt", tx + "/notes.txt"}
}

func golden(t *testing.T, path, got string) {
	t.Helper()
	if *update {
		os.WriteFile(path, []byte(got), 0o644)
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run go test ./internal/draft -update)", err)
	}
	if string(want) != got {
		t.Errorf("%s differs; run go test ./internal/draft -update and review the diff\n%s", path, got)
	}
}

func TestTranscripts(t *testing.T) {
	vtt, err := LoadTranscript(tx + "/call.vtt")
	if err != nil {
		t.Fatal(err)
	}
	if len(vtt) != 3 || vtt[0].Speaker != "Expert A" || vtt[0].Time != "00:00:01" || vtt[1].Speaker != "Writer" || vtt[2].Time != "00:00:18" {
		t.Errorf("vtt: %+v", vtt)
	}
	srt, _ := LoadTranscript(tx + "/interview.srt")
	if len(srt) != 1 || srt[0].Speaker != "Expert B" || srt[0].Time != "00:01:02" || !strings.Contains(srt[0].Text, "2 GB a day") {
		t.Errorf("srt: %+v", srt)
	}
	txt, _ := LoadTranscript(tx + "/notes.txt")
	if len(txt) != 2 || txt[0].Time != "00:03:10" || txt[1].Time != "" || txt[1].Speaker != "Expert B" || txt[1].Line != 3 {
		t.Errorf("txt: %+v", txt)
	}
	if _, err := LoadTranscript("https://example.test/call.vtt"); err == nil {
		t.Error("a URL transcript was accepted")
	}
}

func TestFacts(t *testing.T) {
	got := map[string]bool{}
	for _, f := range ExtractFacts("The default port is 8080. Run `lumen deploy --force` with --timeout=30s on version 2.1, set `log_level` and LUMEN_HOME; it uses 2 GB and 15% CPU.") {
		got[f.Kind+":"+f.Value] = true
	}
	for _, want := range []string{"default:8080", "command:lumen deploy --force", "flag:--force", "flag:--timeout=30s", "version:2.1", "field:log_level", "field:LUMEN_HOME", "number:2 GB", "number:15%", "number:8080"} {
		if !got[want] {
			t.Errorf("missing %s in %v", want, got)
		}
	}
	if got["number:2"] || got["number:1"] {
		t.Errorf("version parts read as numbers: %v", got)
	}
}

func TestStatements(t *testing.T) {
	md := "---\ntitle: x\n---\n\n## A\n\n<!-- guide -->\n\nFirst one, e.g. this.[^s1] Second `a.b` one![^s2][^s3]\n\n- Item without cite.\n\nSource:[^s4]\n\n```\nlumen init\n```\n\n> **[placeholder]** nothing\n\n[^s1]: x\n"
	sts := Statements(md)
	if len(sts) != 4 {
		t.Fatalf("got %d statements: %+v", len(sts), sts)
	}
	if sts[0].Text != "First one, e.g. this." || strings.Join(sts[0].Citations, ",") != "s1" {
		t.Errorf("first: %+v", sts[0])
	}
	if strings.Join(sts[1].Citations, ",") != "s2,s3" || sts[2].Text != "Item without cite." || len(sts[2].Citations) != 0 {
		t.Errorf("second/third: %+v %+v", sts[1], sts[2])
	}
	if !sts[3].Code || sts[3].Text != "lumen init" || strings.Join(sts[3].Citations, ",") != "s4" || sts[3].Section != "A" {
		t.Errorf("code: %+v", sts[3])
	}
}

func generate(t *testing.T, p Provider) (string, *Ledger) {
	t.Helper()
	out := t.TempDir()
	_, err := Generate(context.Background(), Options{Topic: "Deploying lumen", KB: []string{kb}, Transcripts: transcripts(), Out: out, Provider: p, Date: "2026-01-01"})
	if err != nil {
		t.Fatal(err)
	}
	md, _ := os.ReadFile(filepath.Join(out, DraftFile))
	led, err := ReadLedger(filepath.Join(out, LedgerFile))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(out, ReviewFile)); err != nil {
		t.Error(err)
	}
	return string(md), led
}

func TestExtractiveGolden(t *testing.T) {
	md, led := generate(t, nil)
	golden(t, tx+"/expected-draft.md", md)
	for _, want := range []string{"status: draft", "review: required", "claims: claims.json"} {
		if !strings.Contains(md, want) {
			t.Errorf("front matter lacks %q", want)
		}
	}
	for _, c := range led.Claims {
		if c.Uncited || c.Status != Unverified {
			t.Errorf("%s: uncited=%v status=%s", c.ID, c.Uncited, c.Status)
		}
		for _, f := range c.Facts {
			if !f.InSource {
				t.Errorf("%s: extractive fact %q not found in its own source", c.ID, f.Value)
			}
		}
	}
	md2, _ := generate(t, nil)
	if md2 != md {
		t.Error("the same input gave a different draft")
	}
}

// fake is a model that cites one real and one invented source and adds a
// fact that is not in the passages.
type fake struct{ prompts []string }

func (f *fake) Name() string { return "fake" }
func (f *fake) Complete(_ context.Context, system, prompt string) (string, error) {
	f.prompts = append(f.prompts, prompt)
	if strings.Contains(prompt, "Section: Common mistakes") {
		return "## Common mistakes\n\nBack up before you run `lumen deploy --force` on production.[^s26] Keep 3 backups.[^s999] Nobody knows why.", nil
	}
	return "[placeholder] Not covered by the sources.", nil
}

func TestModelDraftFlags(t *testing.T) {
	p := &fake{}
	md, led := generate(t, p)
	if len(p.prompts) == 0 || !strings.Contains(p.prompts[0], "Passages:") {
		t.Fatal("the model was not given passages")
	}
	if !strings.Contains(md, "prompt_version: "+PromptVersion) || strings.Contains(md, "## Common mistakes\n\n## Common") {
		t.Errorf("front matter or heading handling:\n%s", md)
	}
	if !strings.Contains(md, "Nobody knows why. **[uncited]**") {
		t.Errorf("uncited statement not marked:\n%s", md)
	}
	var backup, invented, uncited *Claim
	for i, c := range led.Claims {
		switch {
		case strings.HasPrefix(c.Text, "Back up"):
			backup = &led.Claims[i]
		case strings.HasPrefix(c.Text, "Keep 3"):
			invented = &led.Claims[i]
		case strings.HasPrefix(c.Text, "Nobody"):
			uncited = &led.Claims[i]
		}
	}
	if backup == nil || invented == nil || uncited == nil {
		t.Fatalf("claims: %+v", led.Claims)
	}
	if backup.Uncited || !backup.Facts[0].InSource {
		t.Errorf("backup claim: %+v", backup)
	}
	if !invented.Uncited || strings.Join(invented.Invalid, ",") != "s999" || invented.Facts[0].InSource {
		t.Errorf("invented citation: %+v", invented)
	}
	if !uncited.Uncited {
		t.Errorf("uncited: %+v", uncited)
	}
	if !strings.Contains(md, "[placeholder]") {
		t.Error("sections the model could not cover should be placeholders")
	}
}

func TestSyncAndProblems(t *testing.T) {
	md, led := generate(t, nil)
	for i := range led.Claims {
		led.Claims[i].Status = Verified
	}
	if p := led.Problems(); len(p) != 0 {
		t.Fatalf("problems after verifying all: %v", p)
	}
	edited := strings.Replace(md, "The default port is 8080.", "The default port is 9090.", 1)
	edited = strings.Replace(edited, "Restart the agent after every upgrade to version 2.1 or later.[^s29]\n", "", 1)
	res := led.Sync(edited)
	if len(res.Added) != 1 || len(res.Removed) != 2 {
		t.Errorf("sync: %+v", res)
	}
	if p := led.Problems(); len(p) != 1 || !strings.Contains(p[0], "not verified") {
		t.Errorf("problems: %v", p)
	}
}

func TestProviderGuards(t *testing.T) {
	cases := []struct {
		cfg ProviderConfig
		ok  bool
	}{
		{ProviderConfig{}, true},
		{ProviderConfig{Kind: "ollama", Model: "m"}, true},
		{ProviderConfig{Kind: "ollama", Model: "m", Endpoint: "http://gpu.example.test:11434"}, false},
		{ProviderConfig{Kind: "ollama", Model: "m", Endpoint: "http://gpu.example.test:11434", AllowRemote: true}, true},
		{ProviderConfig{Kind: "openai", Model: "m", Endpoint: "http://127.0.0.1:8080/v1"}, true},
		{ProviderConfig{Kind: "openai", Model: "m", Endpoint: "https://api.example.test/v1"}, false},
		{ProviderConfig{Kind: "anthropic"}, false},
		{ProviderConfig{Kind: "anthropic", AllowRemote: true}, true},
		{ProviderConfig{Kind: "other"}, false},
	}
	for _, c := range cases {
		_, err := NewProvider(c.cfg)
		if (err == nil) != c.ok {
			t.Errorf("%+v: err %v", c.cfg, err)
		}
	}
}

// The HTTP providers against local fakes: no network.
func TestHTTPProviders(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&got)
		switch r.URL.Path {
		case "/api/chat":
			w.Write([]byte(`{"message":{"content":"from ollama"}}`))
		case "/v1/chat/completions":
			w.Write([]byte(`{"choices":[{"message":{"content":"from openai"}}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	for kind, want := range map[string]string{"ollama": "from ollama", "openai": "from openai"} {
		ep := srv.URL
		if kind == "openai" {
			ep += "/v1"
		}
		p, err := NewProvider(ProviderConfig{Kind: kind, Model: "m", Endpoint: ep})
		if err != nil {
			t.Fatal(err)
		}
		out, err := p.Complete(context.Background(), "sys", "prompt")
		if err != nil || out != want {
			t.Errorf("%s: %q %v", kind, out, err)
		}
		if got["model"] != "m" || got["temperature"] == nil && got["options"] == nil {
			t.Errorf("%s request: %v", kind, got)
		}
	}
}

func TestRefusesOutputInsideKB(t *testing.T) {
	_, err := Generate(context.Background(), Options{Topic: "x", KB: []string{kb}, Out: kb + "/drafts"})
	if err == nil {
		t.Error("a draft inside the knowledge base was allowed")
	}
}
