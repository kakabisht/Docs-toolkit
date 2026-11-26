package parse

import (
	"testing"
)

func TestMarkdown(t *testing.T) {
	src := []byte(`---
title: Hello
description: A page.
aliases: [/old/]
---

# Hello

## Getting started

Text with a [link](other.md#part) and ![img](a.png).

## Getting started

` + "```go\nfmt.Println()\n```\n\n```\nnone\n```\n" + `
### Custom {#my-id}

<a id="raw"></a><a href="/raw-link">raw</a>

Reference [style][ref].

[ref]: https://example.com/ref
`)
	p := Markdown(src)
	if p.Title != "Hello" || p.Description != "A page." || len(p.Aliases) != 1 {
		t.Fatalf("front matter: %+v", p)
	}
	wantAnchors := []string{"hello", "getting-started", "getting-started-1", "my-id", "raw"}
	for _, a := range wantAnchors {
		if !p.Anchors[a] {
			t.Errorf("missing anchor %q in %v", a, p.Anchors)
		}
	}
	hrefs := map[string]bool{}
	for _, l := range p.Links {
		hrefs[l.Href] = true
	}
	for _, h := range []string{"other.md#part", "a.png", "/raw-link", "https://example.com/ref"} {
		if !hrefs[h] {
			t.Errorf("missing link %q in %v", h, hrefs)
		}
	}
	if len(p.CodeBlocks) != 2 || p.CodeBlocks[0].Lang != "go" || p.CodeBlocks[1].Lang != "" {
		t.Errorf("code blocks: %+v", p.CodeBlocks)
	}
	// line numbers count the front matter
	for _, l := range p.Links {
		if l.Href == "other.md#part" && l.Line != 11 {
			t.Errorf("link line = %d, want 11", l.Line)
		}
		if l.Href == "other.md#part" && l.Section != "getting-started" {
			t.Errorf("link section = %q", l.Section)
		}
	}
}

func TestSlugify(t *testing.T) {
	cases := map[string]string{
		"Hello World":         "hello-world",
		"What's new in v2.0?": "whats-new-in-v20",
		"  Trim  ":            "trim",
		"`code` and _under_":  "code-and-_under_",
	}
	for in, want := range cases {
		if got := Slugify(in); got != want {
			t.Errorf("Slugify(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestHTMLMainVsNav(t *testing.T) {
	src := []byte(`<!doctype html><html><head><title>T</title><meta name="description" content="D"></head>
<body><nav><a href="/nav">Nav</a></nav>
<main><h1 id="top-title">Title</h1><h2>Sub <a class="anchor" href="#sub">#</a></h2>
<p>Body <a href="/content">content link</a></p>
<pre><code class="language-bash">echo hi
echo bye</code></pre></main>
<footer id="foot"><a href="https://x.example/">x</a></footer></body></html>`)
	p := HTML(src)
	if p.Title != "Title" || p.Description != "D" {
		t.Fatalf("title/desc: %q %q", p.Title, p.Description)
	}
	var content, nav []string
	for _, l := range p.Links {
		if l.Nav {
			nav = append(nav, l.Href)
		} else {
			content = append(content, l.Href)
		}
	}
	if len(content) != 2 || content[1] != "/content" {
		t.Errorf("content links: %v", content)
	}
	if len(nav) != 2 {
		t.Errorf("nav links: %v", nav)
	}
	if !p.Anchors["foot"] || !p.Anchors["top-title"] || !p.Anchors["sub"] {
		t.Errorf("anchors: %v", p.Anchors)
	}
	if len(p.CodeBlocks) != 1 || p.CodeBlocks[0].Lang != "bash" || p.CodeBlocks[0].Lines != 2 {
		t.Errorf("code: %+v", p.CodeBlocks)
	}
}

func TestDiscovery(t *testing.T) {
	links := LLMSTxtLinks([]byte("# X\n\n- [A](https://x/a.md): a\n- [B](/b) \n- [A again](https://x/a.md)\n"))
	if len(links) != 2 {
		t.Errorf("llms links: %v", links)
	}
	pages, maps := Sitemap([]byte(`<?xml version="1.0"?><urlset><url><loc> https://x/a </loc></url><url><loc>https://x/b</loc></url></urlset>`))
	if len(pages) != 2 || pages[0] != "https://x/a" || len(maps) != 0 {
		t.Errorf("sitemap: %v %v", pages, maps)
	}
	_, maps = Sitemap([]byte(`<sitemapindex><sitemap><loc>https://x/s1.xml</loc></sitemap></sitemapindex>`))
	if len(maps) != 1 {
		t.Errorf("sitemap index: %v", maps)
	}
	if s := RobotsSitemaps([]byte("User-agent: *\nSitemap: https://x/sm.xml\n")); len(s) != 1 {
		t.Errorf("robots: %v", s)
	}
}
