// Package mcpctx retrieves authoritative product context over the Model
// Context Protocol, as citable passages, and serves docsbot's own sources as
// an MCP server.
//
// A context server is anything that answers a search: a product knowledge
// base, an API catalogue, release notes, or `docsbot mcp serve --kb`. The
// agent sends it one query per topic and cites what comes back, so each
// passage keeps the server, the tool and the attribution the server gave.
package mcpctx

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/kakabisht/Docs-toolkit/internal/draft"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ServerConfig is one context server in docsbot.yml (agent.mcp).
type ServerConfig struct {
	Name        string            `yaml:"name"`
	Command     []string          `yaml:"command"`      // stdio server: program and arguments
	URL         string            `yaml:"url"`          // Streamable HTTP server
	Tool        string            `yaml:"tool"`         // tool to call (default: the first whose name contains "search")
	QueryArg    string            `yaml:"query_arg"`    // argument that takes the query (default "query")
	Args        map[string]any    `yaml:"args"`         // fixed arguments sent with every call
	Env         map[string]string `yaml:"env"`          // extra environment for a stdio server
	HeaderEnv   map[string]string `yaml:"header_env"`   // HTTP header -> environment variable holding its value
	AllowRemote bool              `yaml:"allow_remote"` // queries name the product's flags and settings; needed for a server off this machine
	MaxResults  int               `yaml:"max_results"`  // passages kept per query (default 5)
}

// Context is a connection to one context server.
type Context struct {
	cfg     ServerConfig
	session *mcp.ClientSession
	tool    string
	// Log records every call, for the run report.
	Log []Call
}

// Call is one tool call and what came back.
type Call struct {
	Server   string `json:"server"`
	Tool     string `json:"tool"`
	Query    string `json:"query"`
	Passages int    `json:"passages"`
	Error    string `json:"error,omitempty"`
}

// Connect starts or reaches the server and picks the search tool.
func Connect(ctx context.Context, c ServerConfig, version string) (*Context, error) {
	if c.Name == "" {
		c.Name = "context"
	}
	var t mcp.Transport
	switch {
	case len(c.Command) > 0 && c.URL != "":
		return nil, fmt.Errorf("mcp server %s: set command or url, not both", c.Name)
	case len(c.Command) > 0:
		cmd := exec.Command(c.Command[0], c.Command[1:]...)
		cmd.Env = os.Environ()
		for k, v := range c.Env {
			cmd.Env = append(cmd.Env, k+"="+v)
		}
		cmd.Stderr = nil // the server's logs stay out of docsbot's output
		t = &mcp.CommandTransport{Command: cmd}
	case c.URL != "":
		if err := localOnly(c.URL, c.AllowRemote); err != nil {
			return nil, fmt.Errorf("mcp server %s: %w", c.Name, err)
		}
		hc := &http.Client{Timeout: 60 * time.Second}
		if len(c.HeaderEnv) > 0 {
			h := http.Header{}
			for k, env := range c.HeaderEnv {
				v := os.Getenv(env)
				if v == "" {
					return nil, fmt.Errorf("mcp server %s: %s is not set (header %s)", c.Name, env, k)
				}
				h.Set(k, v)
			}
			hc.Transport = headerTransport{h, http.DefaultTransport}
		}
		t = &mcp.StreamableClientTransport{Endpoint: c.URL, HTTPClient: hc, DisableStandaloneSSE: true}
	default:
		return nil, fmt.Errorf("mcp server %s: set command (stdio) or url (Streamable HTTP)", c.Name)
	}
	return ConnectTransport(ctx, c, t, version)
}

// ConnectTransport connects over a given transport (tests use in-memory ones).
func ConnectTransport(ctx context.Context, c ServerConfig, t mcp.Transport, version string) (*Context, error) {
	client := mcp.NewClient(&mcp.Implementation{Name: "docsbot", Version: version}, nil)
	s, err := client.Connect(ctx, t, nil)
	if err != nil {
		return nil, fmt.Errorf("mcp server %s: %w", c.Name, err)
	}
	x := &Context{cfg: c, session: s, tool: c.Tool}
	if x.tool == "" {
		tools, err := s.ListTools(ctx, nil)
		if err != nil {
			s.Close()
			return nil, fmt.Errorf("mcp server %s: listing tools: %w", c.Name, err)
		}
		var names []string
		for _, tl := range tools.Tools {
			names = append(names, tl.Name)
			if x.tool == "" && strings.Contains(strings.ToLower(tl.Name), "search") {
				x.tool = tl.Name
			}
		}
		if x.tool == "" {
			s.Close()
			return nil, fmt.Errorf("mcp server %s has no search tool (tools: %s); set tool: in the config", c.Name, strings.Join(names, ", "))
		}
	}
	return x, nil
}

// Name is the server's configured name.
func (x *Context) Name() string { return x.cfg.Name }

// Tool is the tool the queries go to.
func (x *Context) Tool() string { return x.tool }

// Close ends the session (and stops a stdio server).
func (x *Context) Close() error { return x.session.Close() }

// Retrieve sends one query and returns the result as passages. A server
// error is recorded in the log and returned; the caller decides whether a
// draft can go on without the context.
func (x *Context) Retrieve(ctx context.Context, query string) ([]draft.Source, error) {
	args := map[string]any{}
	for k, v := range x.cfg.Args {
		args[k] = v
	}
	qa := x.cfg.QueryArg
	if qa == "" {
		qa = "query"
	}
	args[qa] = query
	call := Call{Server: x.cfg.Name, Tool: x.tool, Query: query}
	res, err := x.session.CallTool(ctx, &mcp.CallToolParams{Name: x.tool, Arguments: args})
	if err == nil && res.IsError {
		err = fmt.Errorf("%s", strings.TrimSpace(textOf(res.Content)))
	}
	if err != nil {
		call.Error = err.Error()
		x.Log = append(x.Log, call)
		return nil, fmt.Errorf("mcp %s/%s: %w", x.cfg.Name, x.tool, err)
	}
	srcs := Passages(x.cfg.Name+"/"+x.tool, res)
	if n := x.cfg.MaxResults; n > 0 && len(srcs) > n {
		srcs = srcs[:n]
	} else if n == 0 && len(srcs) > 5 {
		srcs = srcs[:5]
	}
	call.Passages = len(srcs)
	x.Log = append(x.Log, call)
	return srcs, nil
}

// Passages turns a tool result into citable passages. Each text item is a
// passage; one that starts with a "Source: …" line is attributed to that
// source, and a long item without one is cut into paragraphs. Structured
// results with a list of {text, source|url|title} objects are read as well.
func Passages(origin string, res *mcp.CallToolResult) []draft.Source {
	var out []draft.Source
	add := func(attr, text string) {
		text = strings.TrimSpace(text)
		if text == "" {
			return
		}
		file := "mcp:" + origin
		if attr != "" {
			file += " — " + attr
		}
		if attr == "" && len(text) > 1200 || strings.Contains(text, "```") {
			lead := ""
			for _, p := range draft.SplitPage(file, text) {
				if p.Code {
					p.Heading = strings.TrimSpace(p.Heading + " " + lead) // found by the words that introduce it
				} else {
					lead = p.Text
				}
				p.Kind = "mcp"
				if attr == "" {
					p.File = fmt.Sprintf("%s, passage %d", file, len(out)+1)
				} else {
					p.File = file // prose and the code it introduces share the attribution
				}
				p.Line, p.EndLine = 0, 0
				out = append(out, p)
			}
			return
		}
		out = append(out, draft.Source{Kind: "mcp", File: file, Text: text})
	}
	for _, c := range res.Content {
		t, ok := c.(*mcp.TextContent)
		if !ok {
			continue
		}
		text := t.Text
		if objs := jsonHits(text); objs != nil {
			for _, o := range objs {
				add(o[0], o[1])
			}
			continue
		}
		attr := ""
		if first, rest, ok := strings.Cut(text, "\n"); ok && strings.HasPrefix(first, "Source:") {
			attr, text = strings.TrimSpace(strings.TrimPrefix(first, "Source:")), rest
		}
		add(attr, text)
	}
	if len(out) == 0 && res.StructuredContent != nil {
		if b, err := json.Marshal(res.StructuredContent); err == nil {
			for _, o := range jsonHits(string(b)) {
				add(o[0], o[1])
			}
		}
	}
	return out
}

// jsonHits reads [{"text"|"content"|"snippet": …, "source"|"url"|"title": …}]
// or {"results": [...]}; nil when the text is not such JSON.
func jsonHits(s string) [][2]string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "[") && !strings.HasPrefix(s, "{") {
		return nil
	}
	var v any
	if json.Unmarshal([]byte(s), &v) != nil {
		return nil
	}
	if m, ok := v.(map[string]any); ok {
		for _, k := range []string{"results", "hits", "items", "documents"} {
			if l, ok := m[k]; ok {
				v = l
				break
			}
		}
	}
	list, ok := v.([]any)
	if !ok {
		return nil
	}
	var out [][2]string
	for _, e := range list {
		m, ok := e.(map[string]any)
		if !ok {
			return nil
		}
		text := firstString(m, "text", "content", "snippet", "body")
		if text == "" {
			continue
		}
		out = append(out, [2]string{firstString(m, "source", "url", "path", "title"), text})
	}
	return out
}

func firstString(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if s, ok := m[k].(string); ok && s != "" {
			return s
		}
	}
	return ""
}

func textOf(cs []mcp.Content) string {
	var b strings.Builder
	for _, c := range cs {
		if t, ok := c.(*mcp.TextContent); ok {
			b.WriteString(t.Text + "\n")
		}
	}
	return b.String()
}

type headerTransport struct {
	h    http.Header
	base http.RoundTripper
}

func (t headerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	for k, v := range t.h {
		r.Header[k] = v
	}
	return t.base.RoundTrip(r)
}

func localOnly(endpoint string, allow bool) error {
	u, err := url.Parse(endpoint)
	if err != nil || u.Host == "" {
		return fmt.Errorf("url %q is not a URL", endpoint)
	}
	if allow {
		return nil
	}
	h := u.Hostname()
	if h == "localhost" {
		return nil
	}
	if ip := net.ParseIP(h); ip != nil && ip.IsLoopback() {
		return nil
	}
	return fmt.Errorf("%s is not on this machine, and the queries name the product's flags and settings; set allow_remote: true on this server if that is intended", h)
}
