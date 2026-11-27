package cmd

import (
	"net/http"
	"os"
)

// serve starts the lumen API (synthetic fixture code).
func serve() {
	home := os.Getenv("LUMEN_HOME")
	level := os.Getenv("LUMEN_LOG_LEVEL")
	_, _ = home, level
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", nil)
	mux.HandleFunc("GET /v1/workspaces/{id}", nil)
	mux.HandleFunc("POST /v1/upgrades", nil)
	http.ListenAndServe(":8080", mux)
}
