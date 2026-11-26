package cmd

import (
	"fmt"
	"net/http"

	"github.com/spf13/cobra"

	"github.com/docsbot/docsbot/internal/crawl"
	"github.com/docsbot/docsbot/internal/devserver"
)

func newServeCmd() *cobra.Command {
	var addr string
	c := &cobra.Command{
		Use:    "serve DIR",
		Short:  "Serve a local docs repo as a test website (HTML, .md twins, llms.txt, sitemap)",
		Long:   "Development helper: serves DIR so live-site mode and afdocs can be tried without deploying.",
		Args:   cobra.ExactArgs(1),
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig(firstArg(args))
			if err != nil {
				return err
			}
			srv, err := devserver.New(args[0], crawl.LocalOptions{ContentDir: cfg.ContentDir, StaticDirs: cfg.StaticDirs, Include: cfg.Include, Exclude: cfg.Exclude})
			if err != nil {
				return err
			}
			progress("serving %d pages from %s on http://%s", len(srv.Site.Pages), srv.Site.Content, addr)
			fmt.Fprintln(cmd.OutOrStdout(), "http://"+addr)
			return http.ListenAndServe(addr, srv)
		},
	}
	c.Flags().StringVar(&addr, "addr", "127.0.0.1:8484", "listen address")
	return c
}
