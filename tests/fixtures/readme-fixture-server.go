// Minimal local-only process used by README lifecycle tests. Production
// captures always build cmd/server; this file exists solely to make teardown
// and filesystem-isolation tests fast enough to run on every edit.
package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"
)

func main() {
	_ = flag.Bool("no-browser", false, "accepted for parity with cmd/server")
	flag.Parse()
	port := os.Getenv("PORT")
	if port == "" {
		port = "8765"
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	server := &http.Server{
		Addr:              fmt.Sprintf("127.0.0.1:%s", port),
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	if err := server.ListenAndServe(); err != nil {
		panic(err)
	}
}
