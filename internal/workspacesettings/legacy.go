package workspacesettings

import (
	"strings"
)

// Telling an upgraded workspace what happened to its planning binding
// (FR-185–FR-188).
//
// A workspace from before this change may carry a `workspace-planning` skill
// binding whose config declared planning policy. That config is not migrated
// and has no effect — the clean break is deliberate, because a half-imported
// policy is one nobody can reason about.
//
// But silently ignoring it would be its own problem: somebody configured those
// values, and a workspace that quietly stops honoring them looks broken rather
// than upgraded. So the binding is DETECTED and reported once, without reading
// a single one of its values, and pointed at where policy lives now.

// LegacyPlanningSkillName is the binding this release stopped honoring.
const LegacyPlanningSkillName = "workspace-planning"

// LegacyPlanningNoticeKey marks a workspace as already told, so the notice
// appears once rather than on every settings read (FR-186).
const LegacyPlanningNoticeKey = "workspace_planning_legacy_notice_ack"

// LegacyPlanningNotice reports that a workspace still carries the old binding.
type LegacyPlanningNotice struct {
	// Present is whether the binding is there at all.
	Present bool `json:"present"`
	// Acknowledged is whether this workspace has already been told.
	Acknowledged bool `json:"acknowledged"`
	// BindingID identifies the binding to discard on the next save. It is the
	// only value read out of it: the config is deliberately not inspected,
	// because reading it is the first step toward importing it.
	BindingID string `json:"binding_id,omitempty"`
	// Message is what the user reads.
	Message string `json:"message,omitempty"`
	// SettingsPath is where planning policy lives now (FR-186).
	SettingsPath string `json:"settings_path,omitempty"`
}

// ShouldShow reports whether the UI should render the notice.
func (n LegacyPlanningNotice) ShouldShow() bool { return n.Present && !n.Acknowledged }

// DetectLegacyPlanningBinding looks for the old binding in a workspace's
// shared data.
//
// It reads the binding's NAME and ID and nothing else. Not looking at the
// config is the point: there is no migration, and a function that read those
// values would be one refactor away from applying them.
func DetectLegacyPlanningBinding(sharedData map[string]any, bindings []map[string]any) LegacyPlanningNotice {
	notice := LegacyPlanningNotice{
		Acknowledged: acknowledged(sharedData),
	}

	for _, binding := range bindings {
		name := strings.TrimSpace(stringValue(binding["skill_name"]))
		if name == "" {
			name = strings.TrimSpace(stringValue(binding["skillName"]))
		}
		if !strings.EqualFold(name, LegacyPlanningSkillName) {
			continue
		}
		// A binding with no config was never carrying policy; it is just a
		// skill binding and there is nothing to report.
		config, hasConfig := binding["config"].(map[string]any)
		if !hasConfig || len(config) == 0 {
			continue
		}

		notice.Present = true
		notice.BindingID = strings.TrimSpace(stringValue(binding["id"]))
		break
	}

	if notice.Present {
		notice.Message = "This workspace has an old workspace-planning skill binding. " +
			"Its settings are no longer used — planning policy now lives in " +
			"Workspace Settings, where each control says whether Ori enforces it. " +
			"Nothing was migrated, and the binding will be removed the next time " +
			"you save settings."
		notice.SettingsPath = "#workspace-settings"
	}
	return notice
}

// AcknowledgeLegacyPlanningNotice marks the workspace as told, so the notice
// does not reappear on every read.
func AcknowledgeLegacyPlanningNotice(sharedData map[string]any) map[string]any {
	if sharedData == nil {
		sharedData = map[string]any{}
	}
	sharedData[LegacyPlanningNoticeKey] = true
	return sharedData
}

// DiscardLegacyPlanningBinding removes ONLY the inactive legacy planning
// binding, returning the remaining bindings and whether one was dropped
// (FR-187).
//
// Every other binding is returned untouched, including a workspace-planning
// binding with no config — that one is an ordinary skill binding somebody may
// have made deliberately, and removing it would be deleting the user's work to
// tidy up our own.
func DiscardLegacyPlanningBinding(bindings []map[string]any) ([]map[string]any, bool) {
	kept := make([]map[string]any, 0, len(bindings))
	discarded := false

	for _, binding := range bindings {
		name := strings.TrimSpace(stringValue(binding["skill_name"]))
		if name == "" {
			name = strings.TrimSpace(stringValue(binding["skillName"]))
		}
		config, hasConfig := binding["config"].(map[string]any)

		if strings.EqualFold(name, LegacyPlanningSkillName) && hasConfig && len(config) > 0 {
			discarded = true
			continue
		}
		kept = append(kept, binding)
	}
	return kept, discarded
}

// acknowledged reads the one-time marker.
func acknowledged(sharedData map[string]any) bool {
	if sharedData == nil {
		return false
	}
	value, present := sharedData[LegacyPlanningNoticeKey]
	if !present {
		return false
	}
	flag, ok := value.(bool)
	return ok && flag
}
