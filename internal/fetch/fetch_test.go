package fetch

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestRedirectsAndCache(t *testing.T) {
	var hits atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/a", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/b", 301) })
	mux.HandleFunc("/b", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/c", 302) })
	mux.HandleFunc("/c", func(w http.ResponseWriter, r *http.Request) { hits.Add(1); w.Write([]byte("hello")) })
	mux.HandleFunc("/loop", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/loop", 302) })
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := New(Options{CacheDir: t.TempDir(), CacheTTL: time.Hour, MaxRedirects: 5})
	r := c.Get(context.Background(), srv.URL+"/a")
	if r.Status != 200 || len(r.Chain) != 2 || r.FinalURL != srv.URL+"/c" || string(r.Body) != "hello" {
		t.Fatalf("%+v", r)
	}
	c.Get(context.Background(), srv.URL+"/a")
	if hits.Load() != 1 {
		t.Errorf("cache miss: %d hits", hits.Load())
	}
	if r := c.Get(context.Background(), srv.URL+"/loop"); r.Err == "" {
		t.Error("redirect loop not detected")
	}
}

func TestHeadFallback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.WriteHeader(405)
		}
	}))
	defer srv.Close()
	r := New(Options{}).Check(context.Background(), srv.URL)
	if r.Status != 200 {
		t.Errorf("status %d", r.Status)
	}
}

func TestPerHostDelay(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	c := New(Options{Delay: 50 * time.Millisecond})
	start := time.Now()
	for i := 0; i < 3; i++ {
		c.Get(context.Background(), srv.URL+"/x"+string(rune('a'+i)))
	}
	if el := time.Since(start); el < 100*time.Millisecond {
		t.Errorf("3 requests took %v, want >= 100ms", el)
	}
}
