package workspace

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// This file holds the workspace-facing vocabulary for what a project template
// asks a new workspace to set up: which local directories the user must choose,
// and what automation to install once a choice is confirmed. The types live
// here (rather than in internal/projecttemplates) because a created workspace
// persists them as part of its template provenance, and projecttemplates
// already depends on this package. projecttemplates aliases them so template
// manifests and workspace provenance can never drift apart.
//
// Everything here is inert data. Nothing in this file expands "~", touches the
// filesystem, registers a watcher, or enables a schedule — resolution happens
// only in guided setup, after the user confirms a folder.

// DirectoryRequirement declares one local directory a template needs the user
// to choose during guided setup.
type DirectoryRequirement struct {
	// Key identifies the directory within the template (e.g. "downloads-root").
	// Consuming code defines what the key means; the template system only stores
	// and normalizes it. Always normalized (trimmed, lower-cased).
	Key string `json:"key"`
	// Label is the human-readable name shown in the setup UI (e.g. "Downloads
	// folder"). Falls back to Key when a hand-edited manifest omits it.
	Label string `json:"label,omitempty"`
	// SuggestedPath is the path pre-filled in the folder picker (e.g.
	// "~/Downloads"). It is a suggestion only — never resolved, expanded, or
	// stat-ed here, and never a substitute for the user's confirmed selection.
	SuggestedPath string `json:"suggested_path,omitempty"`
	// AccessDisclosure is the plain-language statement of what Ori may do with
	// the folder once access is granted, shown next to the picker so approval is
	// informed.
	AccessDisclosure string `json:"access_disclosure,omitempty"`
}

// WatchRecipe describes the file watcher a template wants installed for its
// directory after setup confirmation. The watch is always non-recursive: only
// the directory's immediate children are observed, so there is deliberately no
// "recursive" knob a manifest could turn on.
type WatchRecipe struct {
	// Events lists the file events that should wake the automation. Empty means
	// the consumer's default (["create"]).
	Events []string `json:"events,omitempty"`
	// DebounceSeconds is the coalescing window; 0 means the consumer's default.
	DebounceSeconds int `json:"debounce_seconds,omitempty"`
	// ExcludeSubdirectories are immediate child directory names the automation
	// must ignore (e.g. "Filed", a template's own destination folder).
	ExcludeSubdirectories []string `json:"exclude_subdirectories,omitempty"`
}

// DailyScanRecipe describes the daily catch-up run a template wants scheduled
// for its directory, expressed in local wall-clock time so the schedule follows
// the user rather than the server.
type DailyScanRecipe struct {
	// LocalTime is a 24-hour "HH:MM" wall-clock time.
	LocalTime string `json:"local_time"`
	// Timezone is an optional IANA zone name (e.g. "America/New_York"). Empty
	// means the user's own timezone, resolved at setup time.
	Timezone string `json:"timezone,omitempty"`
}

// AutomationRecipe is the post-setup automation a template asks Ori to install
// once the user has confirmed the matching directory requirement.
type AutomationRecipe struct {
	// DirectoryKey names the DirectoryRequirement this recipe automates.
	DirectoryKey string `json:"directory_key"`
	// Watch, when set, requests one non-recursive watcher on the directory.
	Watch *WatchRecipe `json:"watch,omitempty"`
	// DailyScan, when set, requests one daily catch-up run at a local time.
	DailyScan *DailyScanRecipe `json:"daily_scan,omitempty"`
}

// NormalizeLocalTimeOfDay canonicalizes a 24-hour wall-clock time to "HH:MM".
// It is the one parser for local schedule times shared by template automation
// recipes and the features that install them, so a manifest and the schedule it
// produces can never disagree about what "9:5" means.
func NormalizeLocalTimeOfDay(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", errors.New("is missing local_time")
	}
	hourPart, minutePart, ok := strings.Cut(value, ":")
	if !ok {
		return "", fmt.Errorf("has local_time %q, expected HH:MM", value)
	}
	hour, hourErr := strconv.Atoi(hourPart)
	minute, minuteErr := strconv.Atoi(minutePart)
	if hourErr != nil || minuteErr != nil || hour < 0 || hour > 23 || minute < 0 || minute > 59 {
		return "", fmt.Errorf("has local_time %q, expected HH:MM", value)
	}
	return fmt.Sprintf("%02d:%02d", hour, minute), nil
}

// cloneDirectoryRequirements returns a defensive copy.
func cloneDirectoryRequirements(reqs []DirectoryRequirement) []DirectoryRequirement {
	if len(reqs) == 0 {
		return nil
	}
	out := make([]DirectoryRequirement, len(reqs))
	copy(out, reqs)
	return out
}

// cloneAutomationRecipes deep-copies recipes, including their watch/daily-scan
// blocks, so a caller cannot mutate persisted provenance through a shared
// pointer.
func cloneAutomationRecipes(recipes []AutomationRecipe) []AutomationRecipe {
	if len(recipes) == 0 {
		return nil
	}
	out := make([]AutomationRecipe, 0, len(recipes))
	for _, recipe := range recipes {
		cp := AutomationRecipe{DirectoryKey: recipe.DirectoryKey}
		if recipe.Watch != nil {
			watch := *recipe.Watch
			watch.Events = append([]string(nil), recipe.Watch.Events...)
			watch.ExcludeSubdirectories = append([]string(nil), recipe.Watch.ExcludeSubdirectories...)
			cp.Watch = &watch
		}
		if recipe.DailyScan != nil {
			daily := *recipe.DailyScan
			cp.DailyScan = &daily
		}
		out = append(out, cp)
	}
	return out
}

// PendingDirectoryRequirements returns the local directories the workspace's
// originating template asked the user to choose. They are unresolved by
// definition: the setup service records the confirmed selection elsewhere.
func (w *Workspace) PendingDirectoryRequirements() []DirectoryRequirement {
	p := w.GetTemplateProvenance()
	if p == nil {
		return nil
	}
	return p.DirectoryRequirements
}

// TemplateAutomationRecipeFor returns the automation the originating template
// requested for the given directory key, if any. The recipe is a request, not a
// registration: nothing runs until guided setup confirms the directory.
func (w *Workspace) TemplateAutomationRecipeFor(directoryKey string) (AutomationRecipe, bool) {
	p := w.GetTemplateProvenance()
	if p == nil {
		return AutomationRecipe{}, false
	}
	directoryKey = strings.ToLower(strings.TrimSpace(directoryKey))
	for _, recipe := range p.AutomationRecipes {
		if recipe.DirectoryKey == directoryKey {
			return recipe, true
		}
	}
	return AutomationRecipe{}, false
}
