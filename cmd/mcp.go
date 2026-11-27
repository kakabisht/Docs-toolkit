package cmd

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"

	"github.com/kakabisht/Docs-toolkit/internal/draft"
	"github.com/kakabisht/Docs-toolkit/internal/mcpctx"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"
)

func newMCPCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "mcp",
		Short: "Serve documentation and knowledge-base passages over the Model Context Protocol",
	}
	c.AddCommand(newMCPServeCmd())
	return c
}

func newMCPServeCmd() *cobra.Command {
	var kb []string
	var noRepo bool
	var addr string
	c := &cobra.Command{
		Use:   "serve [SOURCE]",
		Short: "Run an MCP server with search_docs and get_passage tools",
		Long: `Serve passages over MCP, for the documentation agent (agent.mcp) or any MCP
client such as Claude Code. The passages are the repository's pages (SOURCE,
default the current folder) and the folders given with --kb; --no-repo
serves only --kb, which makes a product knowledge base the agent can cite.

Tools (both read-only):
  search_docs   {query, limit}: the best passages, each starting with "Source: file, lines"
  get_passage   {file, line}: one passage

Standard input and output by default; --http serves Streamable HTTP on a
local address. Nothing is fetched from the network.`,
		Args: cobra.MaximumNArgs(1),
		Example: `  docsbot mcp serve --no-repo --kb ../product-kb
  docsbot mcp serve ./docs-repo --http 127.0.0.1:8765
  claude mcp add docs -- docsbot mcp serve ~/work/docs-repo`,
		RunE: func(cmd *cobra.Command, args []string) error {
			var srcs []draft.Source
			if !noRepo {
				source := firstNonEmpty(firstArg(args), ".")
				cfg, err := loadConfig(source)
				if err != nil {
					return err
				}
				s, err := localRepo(cmd, cfg, source)
				if err != nil {
					return err
				}
				for _, p := range s.Pages {
					if p.RepoPath != "" {
						srcs = append(srcs, draft.SplitPage(p.RepoPath, string(p.Raw))...)
					}
				}
			}
			more, err := draft.LoadKB(kb)
			if err != nil {
				return &exitError{code: ExitError, msg: "error: " + err.Error()}
			}
			srcs = append(srcs, more...)
			if len(srcs) == 0 {
				return &exitError{code: ExitError, msg: "error: nothing to serve: give a repository or --kb"}
			}
			server := mcpctx.NewServer(srcs, "docsbot", Version)
			fmt.Fprintf(os.Stderr, "docsbot mcp: serving %d passages\n", len(srcs))
			if addr == "" {
				return server.Run(cmd.Context(), &mcp.StdioTransport{})
			}
			host, _, err := net.SplitHostPort(addr)
			if err != nil {
				return &exitError{code: ExitError, msg: "error: --http wants host:port, e.g. 127.0.0.1:8765"}
			}
			if ip := net.ParseIP(host); host != "localhost" && (ip == nil || !ip.IsLoopback()) {
				return &exitError{code: ExitError, msg: "error: --http serves on this machine only (127.0.0.1, ::1 or localhost)"}
			}
			h := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
			srv := &http.Server{Addr: addr, Handler: h}
			go func() {
				<-cmd.Context().Done()
				srv.Shutdown(context.Background())
			}()
			fmt.Fprintf(os.Stderr, "docsbot mcp: http://%s/\n", addr)
			if err := srv.ListenAndServe(); err != http.ErrServerClosed {
				return err
			}
			return nil
		},
	}
	c.Flags().StringSliceVar(&kb, "kb", nil, "folders or files of Markdown, AsciiDoc, reST or text to serve (repeatable)")
	c.Flags().BoolVar(&noRepo, "no-repo", false, "serve only --kb, not the repository's pages")
	c.Flags().StringVar(&addr, "http", "", "serve Streamable HTTP on this local address instead of stdio")
	return c
}
