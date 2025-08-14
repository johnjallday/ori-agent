package pluginhttp

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"plugin"

	"github.com/johnjallday/dolphin-agent/pluginapi"

	"github.com/johnjallday/dolphin-agent/internal/store"
	"github.com/johnjallday/dolphin-agent/internal/types"
)

// ToolLoader abstracts loading a plugin Tool from a path.
type ToolLoader interface {
	Load(path string) (pluginapi.Tool, error)
}

// NativeLoader uses the Go "plugin" package to load .so files.
type NativeLoader struct{}

func (NativeLoader) Load(path string) (pluginapi.Tool, error) {
	p, err := plugin.Open(path)
	if err != nil {
		return nil, err
	}
	sym, err := p.Lookup("Tool")
	if err != nil {
		return nil, err
	}
	tool, ok := sym.(pluginapi.Tool)
	if !ok {
		return nil, errors.New("invalid plugin type: symbol Tool does not implement pluginapi.Tool")
	}
	return tool, nil
}

// Handler serves /api/plugins (GET list, POST upload+load, DELETE unload).
type Handler struct {
	State  store.Store
	Loader ToolLoader
}

func New(state store.Store, loader ToolLoader) *Handler {
	return &Handler{State: state, Loader: loader}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {

	case http.MethodGet:
		h.list(w, r)

	case http.MethodPost:
		h.uploadAndLoad(w, r)

	case http.MethodDelete:
		h.unload(w, r)

	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (h *Handler) list(w http.ResponseWriter, _ *http.Request) {
	_, current := h.State.ListAgents()
	ag, ok := h.State.GetAgent(current)
	if !ok {
		http.Error(w, "current agent not found", http.StatusInternalServerError)
		return
	}
	plist := make([]map[string]string, 0, len(ag.Plugins))
	for name, pl := range ag.Plugins {
		plist = append(plist, map[string]string{
			"name":        name,
			"description": pl.Definition.Description.String(),
		})
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"plugins": plist})
}

func (h *Handler) uploadAndLoad(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(10 << 20); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	file, header, err := r.FormFile("plugin")
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	defer file.Close()

	tmpFile := filepath.Join(os.TempDir(), header.Filename)
	out, err := os.Create(tmpFile)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if _, err := io.Copy(out, file); err != nil {
		out.Close()
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	out.Close()

	tool, err := h.Loader.Load(tmpFile)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	def := tool.Definition()

	_, current := h.State.ListAgents()
	ag, ok := h.State.GetAgent(current)
	if !ok {
		http.Error(w, "current agent not found", http.StatusInternalServerError)
		return
	}
	if ag.Plugins == nil {
		ag.Plugins = map[string]types.LoadedPlugin{}
	}
	ag.Plugins[def.Name] = types.LoadedPlugin{Tool: tool, Definition: def, Path: tmpFile}
	if err := h.State.SetAgent(current, ag); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (h *Handler) unload(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	if name == "" {
		http.Error(w, "name required", http.StatusBadRequest)
		return
	}
	_, current := h.State.ListAgents()
	ag, ok := h.State.GetAgent(current)
	if !ok {
		http.Error(w, "current agent not found", http.StatusInternalServerError)
		return
	}
	delete(ag.Plugins, name)
	if err := h.State.SetAgent(current, ag); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}
