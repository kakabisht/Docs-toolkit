// Package config loads docsbot.yml and merges it with flag values.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is the full set of options. Zero values mean "use the default".
type Config struct {
	// BaseURL is the live site for a local repo, so absolute links to it count as internal
	// and afdocs can run against it.
	BaseURL string `yaml:"base_url"`
	// ContentDir overrides auto-detection of the content root inside a local repo.
	ContentDir string `yaml:"content_dir"`
	// StaticDirs are extra folders searched for images and downloads in local mode.
	StaticDirs   []string `yaml:"static_dirs"`
	NavFile      string   `yaml:"nav_file"`       // navigation source (mkdocs.yml, SUMMARY.md, sidebars.json, a YAML list); auto-detected when empty
	AnchorStyle  string   `yaml:"anchor_style"`   // github | mkdocs | sphinx | kramdown; detected from the generator when empty
	MDXLinkProps []string `yaml:"mdx_link_props"` // JSX props that hold link targets (default href, to)
	Vocabulary   string   `yaml:"vocabulary"`     // controlled vocabulary and alias file
	Include      []string `yaml:"include"`        // glob patterns of pages to keep
	Exclude      []string `yaml:"exclude"`        // glob patterns of pages to drop

	MaxPages    int           `yaml:"max_pages"`
	Follow      *bool         `yaml:"follow"` // follow internal links beyond llms.txt and sitemap (live mode)
	Concurrency int           `yaml:"concurrency"`
	Delay       time.Duration `yaml:"delay"` // minimum delay between requests to the same host
	Timeout     time.Duration `yaml:"timeout"`
	UserAgent   string        `yaml:"user_agent"`
	CacheDir    string        `yaml:"cache_dir"`
	CacheTTL    time.Duration `yaml:"cache_ttl"`
	NoCache     bool          `yaml:"-"`

	File string `yaml:"-"` // the config file that was read

	Links LinksConfig `yaml:"links"`
	Audit AuditConfig `yaml:"audit"`
	Check CheckConfig `yaml:"check"`
}

// CheckConfig tunes `check`.
type CheckConfig struct {
	Rules        map[string]string `yaml:"rules"`         // rule ID -> error | warning | info | off
	FailLevel    string            `yaml:"fail_level"`    // lowest severity that fails the run (default error)
	Schema       string            `yaml:"schema"`        // JSON Schema (JSON or YAML) for front matter
	TodoPatterns []string          `yaml:"todo_patterns"` // regexes for leftover markers (replace the defaults)
	EntryPages   []string          `yaml:"entry_pages"`   // page routes allowed to have no inbound links
}

// LinksConfig tunes the broken link checker.
type LinksConfig struct {
	External        *bool    `yaml:"external"`         // check external URLs (default true)
	ExcludeURLs     []string `yaml:"exclude_urls"`     // regexes of URLs never checked
	IgnoreHrefs     []string `yaml:"ignore_hrefs"`     // regexes of raw hrefs to skip (e.g. render-hook shorthands like "^g$")
	ExternalAnchors bool     `yaml:"external_anchors"` // also verify #fragments on external pages
	FailOn          []string `yaml:"fail_on"`          // problems that fail the run
	MaxRedirects    int      `yaml:"max_redirects"`
	ExternalPerHost int      `yaml:"external_per_host"` // concurrent requests per external host
}

// AuditConfig tunes agent readiness.
type AuditConfig struct {
	Tasks        string  `yaml:"tasks"`         // findability tasks file
	QueryLog     string  `yaml:"query_log"`     // search query log (CSV)
	Afdocs       string  `yaml:"afdocs"`        // auto | on | off
	AfdocsCmd    string  `yaml:"afdocs_cmd"`    // command to run, default "npx -y afdocs@0.23"
	AfdocsWeight float64 `yaml:"afdocs_weight"` // share of the overall score from afdocs (0-1)
	FailUnder    int     `yaml:"fail_under"`    // fail the run if the overall score is below this
	MaxPageChars int     `yaml:"max_page_chars"`
	MaxDepth     int     `yaml:"max_depth"`
}

// Default returns the built-in defaults.
func Default() Config {
	t := true
	return Config{
		MaxPages:    2000,
		Follow:      &t,
		Concurrency: 8,
		Delay:       100 * time.Millisecond,
		Timeout:     20 * time.Second,
		UserAgent:   "docsbot/0.1 (+docs link checker)",
		CacheDir:    defaultCacheDir(),
		CacheTTL:    6 * time.Hour,
		Links: LinksConfig{
			External:        &t,
			FailOn:          []string{"broken", "broken-anchor"},
			MaxRedirects:    10,
			ExternalPerHost: 2,
		},
		Check: CheckConfig{FailLevel: "error"},
		Audit: AuditConfig{
			Afdocs:       "auto",
			AfdocsCmd:    "npx -y afdocs@0.23",
			AfdocsWeight: 0.6,
			MaxPageChars: 50000,
			MaxDepth:     4,
		},
	}
}

func defaultCacheDir() string {
	if d, err := os.UserCacheDir(); err == nil {
		return filepath.Join(d, "docsbot")
	}
	return filepath.Join(os.TempDir(), "docsbot-cache")
}

// Load reads path (or ./docsbot.yml when path is empty) on top of the defaults.
func Load(path string, searchDirs ...string) (Config, error) {
	cfg := Default()
	explicit := path != ""
	if !explicit {
		for _, d := range append([]string{"."}, searchDirs...) {
			for _, c := range []string{"docsbot.yml", "docsbot.yaml", ".docsbot.yml"} {
				p := filepath.Join(d, c)
				if _, err := os.Stat(p); err == nil {
					path = p
					break
				}
			}
			if path != "" {
				break
			}
		}
		if path == "" {
			return cfg, nil
		}
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return cfg, err
	}
	if err := yaml.Unmarshal(b, &cfg); err != nil {
		return cfg, fmt.Errorf("%s: %w", path, err)
	}
	cfg.File = path
	// file paths in the config are relative to the config file
	dir := filepath.Dir(path)
	for _, p := range []*string{&cfg.Vocabulary, &cfg.Check.Schema, &cfg.Audit.Tasks, &cfg.Audit.QueryLog} {
		if *p != "" && !filepath.IsAbs(*p) {
			*p = filepath.Join(dir, *p)
		}
	}
	return cfg, nil
}

// FollowLinks reports whether the live crawler should follow internal links.
func (c Config) FollowLinks() bool { return c.Follow == nil || *c.Follow }

// CheckExternal reports whether external URLs are checked.
func (c Config) CheckExternal() bool { return c.Links.External == nil || *c.Links.External }
