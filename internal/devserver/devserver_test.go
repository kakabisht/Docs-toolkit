package devserver

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/kakabisht/Docs-toolkit/internal/crawl"
)

func TestPreviewServesOnlyShippedLLMS(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "docs"), 0o755)
	os.WriteFile(filepath.Join(dir, "docs", "index.md"), []byte("# Home\n\nHello.\n"), 0o644)
	srv, err := New(dir, crawl.LocalOptions{NoGit: true})
	if err != nil {
		t.Fatal(err)
	}
	get := func(path string) int {
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		return w.Code
	}
	if get("/llms.txt") != 200 {
		t.Error("the dev server should generate llms.txt")
	}
	srv.Preview = true
	if get("/llms.txt") != 404 {
		t.Error("the preview served an llms.txt the repo does not have")
	}
	os.WriteFile(filepath.Join(dir, "docs", "llms.txt"), []byte("# Home\n"), 0o644)
	if get("/llms.txt") != 200 {
		t.Error("the preview did not serve the repo's llms.txt")
	}
}
