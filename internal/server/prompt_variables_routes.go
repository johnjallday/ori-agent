package server

import (
	"net/http"
	"strings"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/promptvars"
)

// handlePromptVariablesList serves GET /api/prompt-variables: the closed prompt
// variable vocabulary in author-facing order, for the template authoring UI's
// variable inserter / reference (PRD FR27).
func (s *Server) handlePromptVariablesList(w http.ResponseWriter, r *http.Request) {
	type variable struct {
		Name        string `json:"name"`
		Kind        string `json:"kind"`
		Fenced      bool   `json:"fenced"`
		Description string `json:"description"`
	}
	specs := promptvars.Vocabulary()
	out := make([]variable, 0, len(specs))
	for _, sp := range specs {
		kind := "scalar"
		if sp.Kind == promptvars.Block {
			kind = "block"
		}
		out = append(out, variable{Name: sp.Name, Kind: kind, Fenced: sp.Fenced, Description: sp.Description})
	}
	_ = orihttp.RespondSuccess(w, map[string]any{"variables": out})
}

// handlePromptVariablesPreview serves POST /api/prompt-variables/preview: resolve
// a prompt against a deterministic synthetic sample workspace so authors can see
// what a variable-bearing prompt produces (PRD FR27/FR28).
//
// Preview safety: values are synthetic (no real workspace data, no vault secrets),
// resolution reuses the same fences/self-omission as runtime, and the resolved
// body is never written to application logs.
func (s *Server) handlePromptVariablesPreview(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Prompt string `json:"prompt"`
	}
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	unknown := promptvars.Unknown(req.Prompt)
	if len(unknown) > 0 {
		_ = orihttp.RespondBadRequest(w, "unknown prompt variable {{"+unknown[0]+"}} — only the documented variables are allowed")
		return
	}

	resolved := promptvars.Resolve(req.Prompt, promptvars.SampleValues())
	_ = orihttp.RespondSuccess(w, map[string]any{
		"had_variables": promptvars.HasVariables(req.Prompt),
		"resolved":      resolved,
		"sample_source": "synthetic", // no real workspace was used
		"note":          strings.TrimSpace("Preview uses a synthetic sample workspace; live runtime uses the real workspace's data and adds context at run time."),
	})
}
