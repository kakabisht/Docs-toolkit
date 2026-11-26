package crawl

import (
	"net/url"
	"path/filepath"
	"testing"

	"github.com/docsbot/docsbot/internal/model"
)

func TestKeys(t *testing.T) {
	for in, want := range map[string]string{
		"/A/B/":         "/a/b",
		"/a/index.html": "/a",
		"":              "/",
		"/a/b?x=1#frag": "/a/b",
		"/a/../c/":      "/c",
	} {
		if got := RouteKey(in); got != want {
			t.Errorf("RouteKey(%q) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[string]string{
		"https://www.Docs.example.com/guide/intro.md": "docs.example.com/guide/intro",
		"https://docs.example.com/guide/intro/":       "docs.example.com/guide/intro",
		"https://docs.example.com/index.md":           "docs.example.com/",
		"https://docs.example.com/a/index.html":       "docs.example.com/a",
	} {
		u, _ := url.Parse(in)
		if got := NormalizeURL(u); got != want {
			t.Errorf("NormalizeURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLocalResolve(t *testing.T) {
	s, err := LoadLocal(filepath.Join("..", "..", "testdata", "site"), LocalOptions{BaseURL: "https://docs.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	install := s.Page("/guide/install")
	cfg := s.Page("/guide/configure")
	if install == nil || cfg == nil {
		t.Fatalf("pages: %v", s.Pages)
	}
	if install.URL != "https://docs.example.com/guide/install" {
		t.Errorf("url: %s", install.URL)
	}
	cases := []struct {
		from   *model.Page
		href   string
		kind   LinkKind
		key    string
		exists bool
		alias  bool
	}{
		{install, "configure.md#options", KindInternal, "/guide/configure", true, false},
		{cfg, "../guide/install.md", KindInternal, "/guide/install", true, false},
		{install, "/api/", KindInternal, "/api", true, false},
		{install, "/quickstart/", KindInternal, "/guide/install", true, true},
		{install, "https://docs.example.com/api/endpoints/", KindInternal, "/api/endpoints", true, false},
		{install, "https://other.example.com/", KindExternal, "", false, false},
		{install, "#requirements", KindAnchor, "/guide/install", true, false},
		{install, "/img/logo.png", KindAsset, "", true, false},
		{install, "/img/gone.png", KindAsset, "", false, false},
		{install, "mailto:a@b.c", KindOther, "", false, false},
		{install, "{{< ref x >}}", KindOther, "", false, false},
		{install, "/nope/", KindInternal, "/nope", false, false},
	}
	for _, c := range cases {
		got := s.Resolve(c.from, c.href)
		if got.Kind != c.kind || got.Exists != c.exists || got.ViaAlias != c.alias || (c.key != "" && got.Key != c.key) {
			t.Errorf("Resolve(%s, %q) = %+v", c.from.Key, c.href, got)
		}
	}
}
