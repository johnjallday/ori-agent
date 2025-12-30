package chathttp

import (
	"encoding/json"
	"net/http"
	"os"
	"time"

	"github.com/johnjallday/ori-agent/internal/logger"
)

// HandleExit handles the /exit command to shut down the server
func (ch *CommandHandler) HandleExit(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// Send acknowledgment response first
	response := map[string]any{
		"response": "👋 **Shutting down ori-agent server...**\n\nGoodbye!",
	}
	if encErr := json.NewEncoder(w).Encode(response); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}

	// Flush the response to ensure client receives it
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}

	// Trigger shutdown in a goroutine to allow response to be sent
	go func() {
		// Small delay to ensure response is sent
		time.Sleep(100 * time.Millisecond)

		if ch.shutdownFunc != nil {
			logger.Info("Executing shutdown via /exit command", logger.Fields{})
			ch.shutdownFunc()
		} else {
			logger.Warn("Shutdown function not set, exiting immediately", logger.Fields{})
			os.Exit(0)
		}
	}()
}
