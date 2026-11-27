package mcpctx

import (
	"context"
	"strings"
	"testing"

	"github.com/kakabisht/Docs-toolkit/internal/draft"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const kb = "../../testdata/fixtures/agent/product-kb"

func connect(t *testing.T, cfg ServerConfig) *Context {
	t.Helper()
	srcs, err := draft.LoadKB([]string{kb})
	if err != nil {
		t.Fatal(err)
	}
	ct, st := mcp.NewInMemoryTransports()
	ctx := context.Background()
	ss, err := NewServer(srcs, "test", "v0").Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ss.Close() })
	x, err := ConnectTransport(ctx, cfg, ct, "v0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { x.Close() })
	return x
}

func TestRetrieve(t *testing.T) {
	x := connect(t, ServerConfig{Name: "kb"})
	if x.Tool() != "search_docs" {
		t.Fatalf("picked tool %q", x.Tool())
	}
	ps, err := x.Retrieve(context.Background(), "--keep-days example")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range ps {
		t.Logf("%s code=%v: %q", p.File, p.Code, p.Text)
	}
	var code, prose bool
	for _, p := range ps {
		if p.Kind != "mcp" || !strings.HasPrefix(p.File, "mcp:kb/search_docs — product-kb/release-notes.md") {
			t.Errorf("attribution: %+v", p)
		}
		code = code || p.Code && strings.Contains(p.Text, "lumen upgrade --backup --keep-days 30")
		prose = prose || !p.Code && strings.Contains(p.Text, "default is 14 days")
	}
	if !code || !prose {
		t.Fatalf("want the example as code and the explanation as prose: %+v", ps)
	}
	if len(x.Log) != 1 || x.Log[0].Passages != len(ps) {
		t.Errorf("log: %+v", x.Log)
	}
}
