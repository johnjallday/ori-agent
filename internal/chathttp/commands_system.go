package chathttp

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/platform"
)

var openApplicationFn = platform.OpenApplication

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

// HandleOpenApp handles /openapp <application-name> and launches the app locally.
func (ch *CommandHandler) HandleOpenApp(w http.ResponseWriter, r *http.Request, appName string) {
	w.Header().Set("Content-Type", "application/json")

	appName = strings.TrimSpace(appName)
	appName = strings.Trim(appName, `"'`)
	if appName == "" {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"response": "❌ Usage: `/openapp <application-name>`\nExample: `/openapp Obsidian`",
		})
		return
	}

	if err := openApplicationFn(appName); err != nil {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"response": "❌ Failed to open application: " + err.Error(),
		})
		return
	}

	_ = json.NewEncoder(w).Encode(map[string]any{
		"response": "✅ Opening " + appName + " now.",
	})
}
