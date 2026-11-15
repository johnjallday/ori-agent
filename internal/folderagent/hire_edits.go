package folderagent

import (
	"context"
	"net/http"
	"reflect"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/johnjallday/ori-agent/internal/personalassistant"
)

// Same name alphabet as the canonical agent-create form/API, with this card's
// narrower length limit applied below.
var hireEditNamePattern = regexp.MustCompile(`^[A-Za-z0-9 _-]+$`)

// HireEdit changes only a future profile's name and model. The map key comes
// from the host-owned HireEntry, never from the editable display name. Role,
// prompt, skills, bindings and grants are deliberately absent.
type HireEdit struct {
	Name     string `json:"name"`
	Provider string `json:"provider"`
	Model    string `json:"model"`
}

// Identity remains stable when a draft is renamed. Older reports without role
// ids use their original, host-issued name until the next look.
func (h HireEntry) Identity() string {
	if h.Key != "" {
		return h.Key
	}
	return h.Name
}

func hireKey(step, roleID string, assistant bool) string {
	if roleID == "" {
		return ""
	}
	kind := "home"
	if assistant {
		kind = "assistant"
	}
	return step + "/" + kind + "/" + roleID
}

func cloneHireEdits(edits map[string]HireEdit) map[string]HireEdit {
	if edits == nil {
		return nil
	}
	out := make(map[string]HireEdit, len(edits))
	for key, edit := range edits {
		out[key] = edit
	}
	return out
}

func applyHireEdits(plan *Plan, edits map[string]HireEdit) {
	for i := range plan.Hires {
		hire := &plan.Hires[i]
		key := hire.Identity()
		edit, ok := edits[key]
		if !ok {
			continue
		}
		oldName := hire.Name
		hire.Key, hire.Name, hire.Provider, hire.ModelID = key, edit.Name, edit.Provider, edit.Model
		hire.Model = NoModelYet
		if edit.Provider != "" && edit.Model != "" {
			hire.Model = edit.Model + " (" + edit.Provider + ")"
		} else {
			// Keep an unfinished edit as a preview, not a consent that would
			// create half a Home before discovering its draft has no model.
			plan.Runnable = false
			plan.Unavailable = "Choose a model for " + hire.Name + " before setting up."
		}
		for j := range plan.Lines {
			line := &plan.Lines[j]
			if line.Step == hire.Step && line.AgentName == oldName {
				line.Name, line.AgentName = "Hires "+hire.Name, hire.Name
			}
		}
	}
}

func hireEditRefusal(code, message string, status int) error {
	return &Refusal{Code: code, Message: message, Status: status}
}

// checkedHireEdits is read-only. A preview never saves an agent or even the
// draft sidecar. Once a run has started its create settings are immutable;
// retries may omit them, but cannot rename/reconfigure a partially made team.
func (s *Service) checkedHireEdits(ctx context.Context, rec Record, selected []string, requested map[string]HireEdit) (map[string]HireEdit, error) {
	if requested == nil {
		requested = rec.HireEdits
	}
	if len(requested) > maxReceiptItems {
		return nil, personalassistant.ErrValidation
	}
	sealed := rec.Setup != nil && (rec.State == StateSetUp || rec.State == StateSettingUp)
	if sealed && (len(requested) != 0 || len(rec.HireEdits) != 0) && !reflect.DeepEqual(requested, rec.HireEdits) {
		return nil, hireEditRefusal("hire_draft_locked", "This setup has already started. Retry its confirmed team, or edit a saved agent in Agents.", http.StatusConflict)
	}
	base := rec
	base.HireEdits = nil
	plan := BuildPlan(base, selected)
	known := make(map[string]HireEntry, len(plan.Hires))
	for _, hire := range plan.Hires {
		known[hire.Identity()] = hire
	}
	out := make(map[string]HireEdit, len(requested))
	for key, raw := range requested {
		hire, ok := known[key]
		if !ok {
			return nil, hireEditRefusal("hire_unknown", "That planned hire is no longer selected. Review the current team.", http.StatusBadRequest)
		}
		if hire.RoleID == "" || hire.Key == "" {
			return nil, hireEditRefusal("hire_not_editable", "Look further to refresh this older report before editing its planned agents.", http.StatusBadRequest)
		}
		edit := HireEdit{Name: strings.TrimSpace(raw.Name), Provider: strings.ToLower(strings.TrimSpace(raw.Provider)), Model: strings.TrimSpace(raw.Model)}
		if !hireEditNamePattern.MatchString(edit.Name) || utf8.RuneCountInString(edit.Name) > AgentNameMaxRunes || strings.ContainsFunc(raw.Name, unicode.IsControl) {
			return nil, hireEditRefusal("agent_name_invalid", "Use 1–60 letters, numbers, spaces, underscores or hyphens for the agent's name.", http.StatusBadRequest)
		}
		if edit.Provider == "" && edit.Model == "" {
			for _, model := range rec.HireModels {
				if model.Default {
					edit.Provider, edit.Model = model.Provider, model.Model
					break
				}
			}
		}
		confirmed, saved := rec.HireEdits[key]
		retained := sealed && saved && confirmed == edit
		// A confirmed model can fall outside the bounded picker catalog after
		// Default changes. Keep its exact identity; the staffing adapters still
		// check the live provider/model before creating anything.
		if !retained && (edit.Provider != "" || edit.Model != "") {
			found := false
			for _, model := range rec.HireModels {
				if model.Provider == edit.Provider && model.Model == edit.Model {
					found = true
					break
				}
			}
			if !found {
				return nil, hireEditRefusal("agent_model_unknown", "That model is not available here. Choose one from the list.", http.StatusBadRequest)
			}
		}
		// A retry can refer to the profile this very run already created. It
		// cannot adopt any other saved profile, even with an identical name.
		if !retained && s.deps.Hiring != nil && s.deps.Hiring.AgentExists(edit.Name) {
			return nil, hireEditRefusal("agent_name_taken", "An agent already has that name. Choose another; setup never replaces an existing agent.", http.StatusConflict)
		}
		out[key] = edit
	}
	seen := make([]string, 0, len(plan.Hires))
	for key, hire := range known {
		name := hire.Name
		if edit, ok := out[key]; ok {
			name = edit.Name
		}
		for _, prior := range seen {
			if strings.EqualFold(prior, name) {
				return nil, hireEditRefusal("agent_name_taken", "Give each planned agent a different name.", http.StatusConflict)
			}
		}
		seen = append(seen, name)
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

// PlanWithHires reviews a pending draft without changing the stored report,
// profiles, Home, reservations or access. Its digest binds material edits.
func (s *Service) PlanWithHires(ctx context.Context, userID, workspaceID string, selected []string, edits map[string]HireEdit) (PlanView, error) {
	rec, err := s.Record(userID, workspaceID)
	if err != nil {
		return PlanView{}, err
	}
	copy := *rec
	copy = *s.effectiveRecord(&copy)
	copy.HireEdits, err = s.checkedHireEdits(ctx, copy, selected, edits)
	if err != nil {
		return PlanView{}, err
	}
	copy.State = StateReported
	return *planViewOf(BuildPlan(copy, selected)), nil
}
