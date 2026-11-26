package parse

import "testing"

func TestSlugStyles(t *testing.T) {
	cases := []struct{ style, in, want string }{
		{StyleGitHub, "Getting Started", "getting-started"},
		{StyleGitHub, "What's new in v2.0?", "whats-new-in-v20"},
		{StyleGitHub, "A  --  B", "a------b"},
		{StyleGitHub, "Café ünïcode", "café-ünïcode"},
		{StyleMkDocs, "A  --  B", "a-b"},
		{StyleMkDocs, "Café ünïcode", "cafe-unicode"},
		{StyleMkDocs, "What's new in v2.0?", "whats-new-in-v20"},
		{StyleSphinx, "2. Install the CLI", "install-the-cli"},
		{StyleSphinx, "Café & Co.", "cafe-co"},
		{StyleKramdown, "1 Intro: Go!", "intro-go"},
	}
	for _, c := range cases {
		if got := SlugifyStyle(c.in, c.style); got != c.want {
			t.Errorf("%s(%q) = %q, want %q", c.style, c.in, got, c.want)
		}
	}
	g := newSlugger(StyleGitHub)
	m := newSlugger(StyleMkDocs)
	for i, want := range []string{"faq", "faq-1", "faq-2"} {
		if got := g.slug("FAQ"); got != want {
			t.Errorf("github dup %d = %q", i, got)
		}
	}
	for i, want := range []string{"faq", "faq_1"} {
		if got := m.slug("FAQ"); got != want {
			t.Errorf("mkdocs dup %d = %q", i, got)
		}
	}
}

func TestAsciiDoc(t *testing.T) {
	src := []byte(`= Install Guide
:product: Fleet
:description: Install {product}.

== Getting Started

See xref:config.adoc#ports[ports], <<getting_started_too,below>>, link:https://example.org[site]
and https://example.com/x[ex]. image::img/arch.png[Architecture]

[[custom]]
== Getting Started

[source,bash]
----
fleet install
----

include::partials/note.adoc[]
`)
	p := AsciiDoc(src, Options{})
	if p.Title != "Install Guide" || p.Description != "Install Fleet." {
		t.Errorf("title/description: %q %q", p.Title, p.Description)
	}
	for _, a := range []string{"_getting_started", "custom"} {
		if !p.Anchors[a] {
			t.Errorf("missing anchor %q in %v", a, p.Anchors)
		}
	}
	hrefs := map[string]bool{}
	for _, l := range p.Links {
		hrefs[l.Href] = true
	}
	for _, h := range []string{"config.adoc#ports", "#getting_started_too", "https://example.org", "https://example.com/x", "img/arch.png"} {
		if !hrefs[h] {
			t.Errorf("missing link %q in %v", h, hrefs)
		}
	}
	if len(p.CodeBlocks) != 1 || p.CodeBlocks[0].Lang != "bash" {
		t.Errorf("code: %+v", p.CodeBlocks)
	}
	if len(p.Includes) != 1 || p.Includes[0].Target != "partials/note.adoc" {
		t.Errorf("includes: %+v", p.Includes)
	}
}
