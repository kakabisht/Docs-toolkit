package mcpctx

import (
	"context"
	"fmt"
	"strings"

	"github.com/kakabisht/Docs-toolkit/internal/draft"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// SearchInput is the search_docs tool's input.
type SearchInput struct {
	Query string `json:"query" jsonschema:"words to search for, such as a flag, setting or task"`
	Limit int    `json:"limit,omitempty" jsonschema:"most passages to return (default 5, at most 20)"`
}

// PassageInput is the get_passage tool's input.
type PassageInput struct {
	File string `json:"file" jsonschema:"file path as search_docs gave it"`
	Line int    `json:"line" jsonschema:"first line of the passage"`
}

// NewServer serves passages as an MCP server with two read-only tools:
// search_docs (BM25 over the passages, the ranking drafts use) and
// get_passage (one passage by file and line). Each result starts with a
// "Source: file, lines" line so clients can cite it.
func NewServer(srcs []draft.Source, name, version string) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: name, Version: version}, &mcp.ServerOptions{
		Instructions: "Product documentation and knowledge base passages. Use search_docs to find passages; cite the Source line of each.",
	})
	readOnly := &mcp.ToolAnnotations{ReadOnlyHint: true}
	mcp.AddTool(s, &mcp.Tool{Name: "search_docs", Description: fmt.Sprintf("Search %d passages of product documentation. Returns the best matches, each starting with a Source: line.", len(srcs)), Annotations: readOnly},
		func(ctx context.Context, _ *mcp.CallToolRequest, in SearchInput) (*mcp.CallToolResult, any, error) {
			if strings.TrimSpace(in.Query) == "" {
				return nil, nil, fmt.Errorf("query is empty")
			}
			n := in.Limit
			if n <= 0 {
				n = 5
			}
			n = min(n, 20)
			res := &mcp.CallToolResult{}
			for _, h := range draft.Search(srcs, in.Query, n) {
				res.Content = append(res.Content, &mcp.TextContent{Text: "Source: " + h.Location() + "\n\n" + withCode(srcs, h)})
			}
			if len(res.Content) == 0 {
				res.Content = []mcp.Content{&mcp.TextContent{Text: "No passage matches."}}
			}
			return res, nil, nil
		})
	mcp.AddTool(s, &mcp.Tool{Name: "get_passage", Description: "Return one passage by the file and first line a search result gave.", Annotations: readOnly},
		func(ctx context.Context, _ *mcp.CallToolRequest, in PassageInput) (*mcp.CallToolResult, any, error) {
			for _, p := range srcs {
				if p.File == in.File && p.Line == in.Line {
					text := p.Text
					if p.Code {
						text = fence(text)
					}
					return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "Source: " + p.Location() + "\n\n" + text}}}, nil, nil
				}
			}
			return nil, nil, fmt.Errorf("no passage at %s line %d", in.File, in.Line)
		})
	return s
}

// withCode returns a passage's text, followed by the code blocks it leads
// into ("Send the id in the body:"), so a search result keeps an example
// together with the sentence that explains it.
func withCode(srcs []draft.Source, h draft.Source) string {
	text := h.Text
	if h.Code {
		return fence(text)
	}
	if !strings.HasSuffix(strings.TrimSpace(text), ":") {
		return text
	}
	var i int
	if _, err := fmt.Sscanf(h.ID, "s%d", &i); err != nil || i < 1 || i > len(srcs) {
		return text
	}
	for j := i; j < len(srcs) && j < i+2; j++ { // srcs[i] is the passage after h
		next := srcs[j]
		if !next.Code || next.File != h.File {
			break
		}
		text += "\n\n" + fence(next.Text)
	}
	return text
}

// fence writes a code passage as a fenced block, so clients can tell it from prose.
func fence(code string) string {
	f := "```"
	for strings.Contains(code, f) {
		f += "`"
	}
	return f + "\n" + strings.TrimRight(code, "\n") + "\n" + f
}
