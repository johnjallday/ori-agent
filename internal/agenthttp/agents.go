package agenthttp

import (
	"encoding/json"
	"log"
	"net/http"

	"github.com/johnjallday/ori-agent/internal/store"
)

type Handler struct {
	State store.Store
}

func New(state store.Store) *Handler { return &Handler{State: state} }

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		names, current := h.State.ListAgents()
		_ = json.NewEncoder(w).Encode(map[string]any{
			"agents":  names,
			"current": current,
		})

	case http.MethodPost:
		var req struct {
			Name string `json:"name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			errMsg := "Failed to decode request: " + err.Error()
			log.Printf("❌ CreateAgent decode error: %s", errMsg)
			http.Error(w, errMsg, http.StatusBadRequest)
			return
		}
		log.Printf("📝 CreateAgent request: name=%q", req.Name)
		if req.Name == "" {
			log.Printf("❌ CreateAgent error: name is empty")
			http.Error(w, "name required", http.StatusBadRequest)
			return
		}
		log.Printf("🔄 Creating agent: %s", req.Name)
		if err := h.State.CreateAgent(req.Name); err != nil {
			log.Printf("❌ CreateAgent error: %v", err)
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		log.Printf("✅ Agent created successfully: %s", req.Name)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]any{
			"success": true,
			"agent":   req.Name,
		})

	case http.MethodPut:
		name := r.URL.Query().Get("name")
		if name == "" {
			http.Error(w, "name required", http.StatusBadRequest)
			return
		}
		if err := h.State.SwitchAgent(name); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusOK)

	case http.MethodDelete:
		name := r.URL.Query().Get("name")
		if name == "" {
			http.Error(w, "name required", http.StatusBadRequest)
			return
		}
		if err := h.State.DeleteAgent(name); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusOK)

	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}
