// Package cmd wires the docsbot commands.
package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"

	"github.com/docsbot/docsbot/internal/config"
	"github.com/docsbot/docsbot/internal/crawl"
	"github.com/docsbot/docsbot/internal/fetch"
	"github.com/docsbot/docsbot/internal/finding"
	"github.com/docsbot/docsbot/internal/graph"
)

// Version is set at build time with -ldflags "-X github.com/docsbot/docsbot/cmd.Version=..."
var Version = "dev"

// Exit codes.
const (
	ExitOK       = 0
	ExitFindings = 1 // the check ran and found problems over the threshold
	ExitError    = 2 // bad input or a runtime failure
)

type exitError struct {
	code int
	msg  string
}

func (e *exitError) Error() string { return e.msg }

func findings(format string, a ...any) error {
	return &exitError{code: ExitFindings, msg: fmt.Sprintf(format, a...)}
}

type globalFlags struct {
	config      string
	format      string
	output      string
	noCache     bool
	quiet       bool
	concurrency int
	maxPages    int
	baseURL     string
	contentDir  string
	include     []string
	exclude     []string
	noFollow    bool
	timeout     time.Duration
	delay       time.Duration
}

var gf globalFlags

func newRoot() *cobra.Command {
	root := &cobra.Command{
		Use:   "docsbot",
		Short: "Audit docs for AI agents: readiness score, knowledge graph and broken links",
		Long: `docsbot reads a live docs site (llms.txt, sitemap, crawl) or a local docs repo
(Markdown, MDX, HTML) and:

  audit   scores agent readiness (afdocs + whole-site graph checks)
  graph   builds a knowledge graph of pages, sections and links (SQLite)
  links   finds broken links, missing anchors, redirects and dead external URLs

SOURCE is a URL (https://docs.example.com) or a directory (./docs).`,
		Version:       Version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	pf := root.PersistentFlags()
	pf.StringVarP(&gf.config, "config", "c", "", "config file (default ./docsbot.yml if present)")
	pf.StringVarP(&gf.format, "format", "f", "text", "output format: text or json; check also takes github, sarif, junit; graph export and view take their own (see their help)")
	pf.StringVarP(&gf.output, "output", "o", "", "write output to a file instead of stdout")
	pf.BoolVar(&gf.noCache, "no-cache", false, "don't read or write the HTTP cache")
	pf.BoolVarP(&gf.quiet, "quiet", "q", false, "no progress output on stderr")
	pf.IntVar(&gf.concurrency, "concurrency", 0, "max concurrent HTTP requests (default 8)")
	pf.IntVar(&gf.maxPages, "max-pages", 0, "stop after this many pages (default 2000)")
	pf.StringVar(&gf.baseURL, "base-url", "", "live site URL for a local repo: absolute links to it count as internal, and audit runs afdocs on it")
	pf.StringVar(&gf.contentDir, "content-dir", "", "content root inside a local repo (default: auto-detect content/, docs/ ...)")
	pf.StringSliceVar(&gf.include, "include", nil, "only pages matching these globs (path relative to content root, or URL path)")
	pf.StringSliceVar(&gf.exclude, "exclude", nil, "skip pages matching these globs")
	pf.BoolVar(&gf.noFollow, "no-follow", false, "live sites: only read pages listed in llms.txt and the sitemap")
	pf.DurationVar(&gf.timeout, "timeout", 0, "per-request timeout (default 20s)")
	pf.DurationVar(&gf.delay, "delay", -1, "minimum delay between requests to one host (default 100ms)")

	root.AddCommand(newCheckCmd(), newLinksCmd(), newRulesCmd(), newGraphCmd(), newAuditCmd(), newServeCmd())
	return root
}

// Execute runs the CLI and returns the process exit code.
func Execute() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	root := newRoot()
	if err := root.ExecuteContext(ctx); err != nil {
		var ee *exitError
		if errors.As(err, &ee) {
			if ee.msg != "" {
				fmt.Fprintln(os.Stderr, ee.msg)
			}
			return ee.code
		}
		fmt.Fprintln(os.Stderr, "error:", err)
		return ExitError
	}
	return ExitOK
}

// loadConfig merges the config file with flags.
func loadConfig(sources ...string) (config.Config, error) {
	var dirs []string
	for _, src := range sources {
		if src != "" && !isURL(src) {
			if fi, err := os.Stat(src); err == nil && !fi.IsDir() {
				src = filepath.Dir(src)
			}
			dirs = append(dirs, src)
		}
	}
	cfg, err := config.Load(gf.config, dirs...)
	if err != nil {
		return cfg, fmt.Errorf("config: %w", err)
	}
	if gf.concurrency > 0 {
		cfg.Concurrency = gf.concurrency
	}
	if gf.maxPages > 0 {
		cfg.MaxPages = gf.maxPages
	}
	if gf.baseURL != "" {
		cfg.BaseURL = gf.baseURL
	}
	if gf.contentDir != "" {
		cfg.ContentDir = gf.contentDir
	}
	cfg.Include = append(cfg.Include, gf.include...)
	cfg.Exclude = append(cfg.Exclude, gf.exclude...)
	if gf.noFollow {
		f := false
		cfg.Follow = &f
	}
	if gf.timeout > 0 {
		cfg.Timeout = gf.timeout
	}
	if gf.delay >= 0 {
		cfg.Delay = gf.delay
	}
	cfg.NoCache = gf.noCache
	return cfg, nil
}

func newClient(cfg config.Config) *fetch.Client {
	cacheDir := cfg.CacheDir
	if cfg.NoCache {
		cacheDir = ""
	}
	return fetch.New(fetch.Options{
		Concurrency:  cfg.Concurrency,
		Delay:        cfg.Delay,
		Timeout:      cfg.Timeout,
		UserAgent:    cfg.UserAgent,
		CacheDir:     cacheDir,
		CacheTTL:     cfg.CacheTTL,
		MaxRedirects: cfg.Links.MaxRedirects,
	})
}

func isURL(s string) bool {
	return strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://")
}

// loadSite crawls a live site or reads a local repo.
func loadSite(ctx context.Context, cfg config.Config, client *fetch.Client, source string) (*crawl.Site, error) {
	start := time.Now()
	var s *crawl.Site
	var err error
	if isURL(source) {
		progress("discovering pages on %s", source)
		s, err = crawl.LoadLive(ctx, client, source, crawl.LiveOptions{
			MaxPages: cfg.MaxPages, Follow: cfg.FollowLinks(), Workers: cfg.Concurrency,
			Include: cfg.Include, Exclude: cfg.Exclude,
			Progress: throttle(func(done, queued int) { progress("  fetched %d pages (%d queued)", done, queued) }),
		})
	} else {
		s, err = crawl.LoadLocal(source, crawl.LocalOptions{
			ContentDir: cfg.ContentDir, StaticDirs: cfg.StaticDirs, BaseURL: cfg.BaseURL,
			Include: cfg.Include, Exclude: cfg.Exclude, MaxPages: cfg.MaxPages,
			AnchorStyle: cfg.AnchorStyle, NavFile: cfg.NavFile, MDXLinkProps: cfg.MDXLinkProps,
		})
	}
	if err != nil {
		return nil, err
	}
	if len(s.Pages) == 0 {
		return nil, fmt.Errorf("no pages found in %s", source)
	}
	where := ""
	if s.Mode == crawl.Local {
		where = fmt.Sprintf(" (content root %s, nav from %s, %s anchors)", s.Content, s.Nav.Source, s.AnchorStyle)
		if s.Generator != "" {
			where = fmt.Sprintf(" (%s site, content root %s, nav from %s, %s anchors)", s.Generator, s.Content, s.Nav.Source, s.AnchorStyle)
		}
	}
	progress("read %d pages%s in %s", len(s.Pages), where, time.Since(start).Round(time.Millisecond))
	return s, nil
}

// loadGraph builds a graph from SOURCE, or loads --db when no source is given.
func loadGraph(ctx context.Context, cfg config.Config, client *fetch.Client, args []string, db string) (*graph.Graph, error) {
	if len(args) == 0 {
		if db == "" {
			return nil, &exitError{code: ExitError, msg: "error: give a SOURCE (URL or directory) or --db"}
		}
		return graph.Load(db)
	}
	s, err := loadSite(ctx, cfg, client, args[0])
	if err != nil {
		return nil, err
	}
	return graph.Build(s), nil
}

func progress(format string, a ...any) {
	if gf.quiet {
		return
	}
	fmt.Fprintf(os.Stderr, format+"\n", a...)
}

// out returns the output writer and a close func.
func out() (io.Writer, func() error, error) {
	if gf.output == "" || gf.output == "-" {
		return os.Stdout, func() error { return nil }, nil
	}
	f, err := os.Create(gf.output)
	if err != nil {
		return nil, nil, err
	}
	return f, f.Close, nil
}

// throttle limits a progress callback to one call every two seconds.
func throttle(f func(a, b int)) func(a, b int) {
	var mu sync.Mutex
	last := time.Now()
	return func(a, b int) {
		mu.Lock()
		defer mu.Unlock()
		if time.Since(last) >= 2*time.Second {
			last = time.Now()
			f(a, b)
		}
	}
}

// findingFormat rejects formats the finding reporters don't know.
func findingFormat() error {
	for _, f := range finding.Formats {
		if f == gf.format {
			return nil
		}
	}
	return &exitError{code: ExitError, msg: "error: --format must be one of " + strings.Join(finding.Formats, ", ")}
}

// textOrJSON rejects CI-only formats for commands that don't produce findings.
func textOrJSON() error {
	if gf.format != "text" && gf.format != "json" {
		return &exitError{code: ExitError, msg: "error: this command supports --format text or json"}
	}
	return nil
}

func firstArg(args []string) string {
	if len(args) > 0 {
		return args[0]
	}
	return ""
}
