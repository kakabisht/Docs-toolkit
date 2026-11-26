package search

import (
	"testing"

	"github.com/kakabisht/Docs-toolkit/internal/model"
)

func TestStem(t *testing.T) {
	for in, want := range map[string]string{
		"configuring": "configur", "configured": "configur", "configuration": "configur", "configure": "configur",
		"installs": "install", "installing": "install", "libraries": "library", "process": "process",
		"status": "status", "boxes": "box", "running": "run", "k8s": "k8s", "analyse": "analys", "analysing": "analys",
	} {
		if got := Stem(in); got != want {
			t.Errorf("Stem(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSearch(t *testing.T) {
	pages := []*model.Page{
		{Key: "/install", Title: "Install on Windows", Paragraphs: []model.Paragraph{{Text: "Download the installer and run it."}}},
		{Key: "/configure", Title: "Configure the server", Paragraphs: []model.Paragraph{{Text: "Set server.port in the configuration file."}}},
		{Key: "/faq", Title: "FAQ", Paragraphs: []model.Paragraph{{Text: "Windows users ask about installing often."}}},
	}
	ix := Build(pages)
	if r := ix.Rank("installing windows", "/install", 10); r != 1 {
		t.Errorf("install rank %d", r)
	}
	if r := ix.Rank("port", "/configure", 10); r != 1 {
		t.Errorf("dotted key part: rank %d", r)
	}
	if hits := ix.Search("kubernetes", 10); len(hits) != 0 {
		t.Errorf("unexpected hits %v", hits)
	}
}
