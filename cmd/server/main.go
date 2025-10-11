package main

import (
	"log"

	"github.com/johnjallday/dolphin-agent/internal/server"
)

func main() {
	// Create server with all dependencies
	srv, err := server.New()
	if err != nil {
		log.Fatalf("Failed to initialize server: %v", err)
	}

	// Start HTTP server
	addr := ":8080"
	log.Printf("Listening on http://localhost%s", addr)
	log.Fatal(srv.HTTPServer(addr).ListenAndServe())
}
