// Package devserver serves a local docs repo as a small live website
// (HTML pages, .md twins, llms.txt, sitemap.xml, alias redirects). It exists
// so live-site mode and afdocs can be exercised end to end without the network.
package devserver

import (
	"bytes"
	"fmt"
	"html"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	ghtml "github.com/yuin/goldmark/renderer/html"
	xhtml "golang.org/x/net/html"

	"github.com/docsbot/docsbot/internal/crawl"
	"github.com/docsbot/docsbot/internal/model"
	"github.com/docsbot/docsbot/internal/parse"
)

var md = goldmark.New(
	goldmark.WithExtensions(extension.GFM, extension.DefinitionList),
	goldmark.WithParserOptions(parser.WithAutoHeadingID(), parser.WithAttribute()),
	goldmark.WithRendererOptions(ghtml.WithUnsafe()),
)

var shortcode = regexp.MustCompile(`\{\{[<%].*?[%>]\}\}`)

// Server serves one site.
type Server struct {
	Site  *crawl.Site
	pages map[string]*model.Page // route key -> page
	alias map[string]string      // alias route -> canonical route
}

// New loads dir and prepares the routes.
func New(dir string, opt crawl.LocalOptions) (*Server, error) {
	s, err := crawl.LoadLocal(dir, opt)
	if err != nil {
		return nil, err
	}
	srv := &Server{Site: s, pages: map[string]*model.Page{}, alias: map[string]string{}}
	for _, p := range s.Pages {
		if p.Draft {
			continue
		}
		srv.pages[p.Key] = p
		for _, a := range p.Aliases {
			srv.alias[crawl.RouteKey(a)] = p.Key
		}
	}
	return srv, nil
}

func (srv *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	switch path {
	case "/llms.txt":
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		srv.llms(w, r)
		return
	case "/sitemap.xml":
		w.Header().Set("Content-Type", "application/xml")
		srv.sitemap(w, r)
		return
	case "/robots.txt":
		fmt.Fprintf(w, "User-agent: *\nAllow: /\nSitemap: %s/sitemap.xml\n", origin(r))
		return
	}
	if strings.HasSuffix(path, ".md") {
		key := crawl.RouteKey(strings.TrimSuffix(path, ".md"))
		if strings.HasSuffix(path, "/index.md") {
			key = crawl.RouteKey(strings.TrimSuffix(path, "/index.md"))
		}
		if p := srv.pages[key]; p != nil {
			b, _ := os.ReadFile(filepath.Join(srv.Site.Root, srv.Site.Content, filepath.FromSlash(p.Path)))
			_, body, _ := parse.SplitFrontMatter(b)
			w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
			fmt.Fprintf(w, "# %s\n\n%s", p.Title, shortcode.ReplaceAll(body, nil))
			return
		}
	}
	key := crawl.RouteKey(path)
	if p := srv.pages[key]; p != nil {
		if !strings.HasSuffix(path, "/") && key != "/" {
			http.Redirect(w, r, path+"/", http.StatusMovedPermanently)
			return
		}
		srv.page(w, p)
		return
	}
	if to, ok := srv.alias[key]; ok {
		http.Redirect(w, r, to+"/", http.StatusMovedPermanently)
		return
	}
	for _, d := range append([]string{srv.Site.Content}, srv.Site.Static...) {
		f := filepath.Join(srv.Site.Root, d, filepath.FromSlash(strings.TrimPrefix(path, "/")))
		if fi, err := os.Stat(f); err == nil && !fi.IsDir() {
			http.ServeFile(w, r, f)
			return
		}
	}
	http.NotFound(w, r)
}

func origin(r *http.Request) string { return "http://" + r.Host }

func (srv *Server) keys() []string {
	ks := make([]string, 0, len(srv.pages))
	for k := range srv.pages {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

func (srv *Server) llms(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintf(w, "# %s\n\n> Docs served by docsbot devserver.\n\n## Pages\n\n", srv.title("/"))
	for _, k := range srv.keys() {
		p := srv.pages[k]
		mdPath := k + ".md"
		if k == "/" {
			mdPath = "/index.md"
		}
		line := fmt.Sprintf("- [%s](%s%s)", p.Title, origin(r), mdPath)
		if p.Description != "" {
			line += ": " + strings.Join(strings.Fields(p.Description), " ")
		}
		fmt.Fprintln(w, line)
	}
}

func (srv *Server) sitemap(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintln(w, `<?xml version="1.0" encoding="UTF-8"?>`)
	fmt.Fprintln(w, `<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">`)
	for _, k := range srv.keys() {
		loc := origin(r) + k
		if k != "/" {
			loc += "/"
		}
		fmt.Fprintf(w, "  <url><loc>%s</loc></url>\n", html.EscapeString(loc))
	}
	fmt.Fprintln(w, `</urlset>`)
}

func (srv *Server) title(k string) string {
	if p := srv.pages[k]; p != nil && p.Title != "" {
		return p.Title
	}
	return "Docs"
}

func (srv *Server) page(w http.ResponseWriter, p *model.Page) {
	b, err := os.ReadFile(filepath.Join(srv.Site.Root, srv.Site.Content, filepath.FromSlash(p.Path)))
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	var content []byte
	if p.Format == "html" {
		content = b
	} else {
		_, body, _ := parse.SplitFrontMatter(b)
		var buf bytes.Buffer
		if err := md.Convert(shortcode.ReplaceAll(body, nil), &buf); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		content = srv.rewriteLinks(p, buf.Bytes())
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, "<!DOCTYPE html>\n<html lang=\"en\"><head><meta charset=\"utf-8\"><title>%s</title>", html.EscapeString(p.Title))
	if p.Description != "" {
		fmt.Fprintf(w, "<meta name=\"description\" content=\"%s\">", html.EscapeString(p.Description))
	}
	fmt.Fprint(w, "</head><body><header><nav><a href=\"/\">Home</a>")
	for _, k := range srv.keys() {
		if strings.Count(k, "/") == 1 && k != "/" && crawl.IsIndexFile(srv.pages[k].Path) {
			fmt.Fprintf(w, " <a href=\"%s/\">%s</a>", k, html.EscapeString(srv.pages[k].Title))
		}
	}
	fmt.Fprintf(w, "</nav></header><main><h1>%s</h1>\n", html.EscapeString(p.Title))
	w.Write(content)
	if crawl.IsIndexFile(p.Path) {
		// section listing, as generators render for index pages
		fmt.Fprint(w, "\n<ul class=\"section-list\">")
		prefix := strings.TrimSuffix(p.Key, "/") + "/"
		for _, k := range srv.keys() {
			rest, ok := strings.CutPrefix(k, prefix)
			if !ok || rest == "" || strings.Contains(rest, "/") {
				continue
			}
			fmt.Fprintf(w, "<li><a href=\"%s/\">%s</a></li>", k, html.EscapeString(srv.pages[k].Title))
		}
		fmt.Fprint(w, "</ul>")
	}
	fmt.Fprint(w, "</main></body></html>")
}

// rewriteLinks maps source hrefs (../foo.md, /section/) to published URLs,
// the way a generator's link render hook would.
func (srv *Server) rewriteLinks(p *model.Page, src []byte) []byte {
	z := xhtml.NewTokenizer(bytes.NewReader(src))
	var out bytes.Buffer
	for {
		tt := z.Next()
		if tt == xhtml.ErrorToken {
			return out.Bytes()
		}
		t := z.Token()
		if (tt == xhtml.StartTagToken || tt == xhtml.SelfClosingTagToken) && (t.Data == "a" || t.Data == "img") {
			for i, a := range t.Attr {
				if a.Key != "href" && a.Key != "src" {
					continue
				}
				tg := srv.Site.Resolve(p, a.Val)
				if tg.Kind == crawl.KindInternal && tg.Exists && tg.Page != nil {
					u := tg.Page.Key
					if u != "/" {
						u += "/"
					}
					if tg.Fragment != "" {
						u += "#" + tg.Fragment
					}
					t.Attr[i].Val = u
				}
			}
		}
		out.WriteString(t.String())
	}
}
