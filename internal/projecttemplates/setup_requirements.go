package projecttemplates

import (
	"errors"
	"fmt"
	"path"
	"slices"
	"strings"
	"time"

	workspace "github.com/johnjallday/ori-agent/internal/workspace"
)

// ErrInvalidDirectoryRequirements reports a directory-requirement edit with a
// blank key, a blank label, a duplicate key, or an unusable suggested path.
var ErrInvalidDirectoryRequirements = errors.New("invalid directory requirements")

// ErrInvalidAutomationRecipes reports an automation-recipe edit that names an
// undeclared directory key, repeats a directory key, declares no automation, or
// carries an out-of-range event, debounce, time, or timezone value.
var ErrInvalidAutomationRecipes = errors.New("invalid automation recipes")

// The setup-requirement types are declared in internal/workspace (alongside
// TemplateProvenance, which persists them into a created workspace) and aliased
// here so a template manifest and a workspace's recorded requirements can never
// drift apart. This file owns the authoring rules: validation for saves and
// lenient normalization for loads. Like CapabilityRequirement, the data is
// inert — nothing here expands "~", touches the filesystem, or picks a path.

// DirectoryRequirement declares one local directory a template needs the user
// to choose during guided setup.
type DirectoryRequirement = workspace.DirectoryRequirement

// ValidRecipeFileEvents is the accepted set of watcher event names, matching
// the file-watch trigger source's vocabulary (see internal/trigger).
var ValidRecipeFileEvents = []string{"create", "modify", "remove", "rename"}

// maxRecipeDebounceSeconds bounds a recipe's coalescing window at one hour so a
// hand-edited manifest cannot park automation indefinitely.
const maxRecipeDebounceSeconds = 3600

// WatchRecipe describes the non-recursive file watcher a template wants
// installed for its directory after setup confirmation.
type WatchRecipe = workspace.WatchRecipe

// DailyScanRecipe describes the daily catch-up run a template wants scheduled
// for its directory, in local wall-clock time.
type DailyScanRecipe = workspace.DailyScanRecipe

// AutomationRecipe is the post-setup automation a template asks Ori to install
// once the user has confirmed the matching directory requirement. It is inert
// data: no watcher is registered and no schedule is enabled until setup
// completes.
type AutomationRecipe = workspace.AutomationRecipe

// validateDirectoryRequirements enforces authoring-save invariants on a raw
// (pre-normalization) edit. Mirrors validateCapabilityRequirements: normalize
// is lenient so loading a hand-edited manifest never fails, while the authoring
// save path returns an error instead of silently dropping bad data.
func validateDirectoryRequirements(reqs []DirectoryRequirement) error {
	seen := make(map[string]bool, len(reqs))
	for _, req := range reqs {
		key := strings.ToLower(strings.TrimSpace(req.Key))
		if key == "" {
			return fmt.Errorf("%w: directory key is required", ErrInvalidDirectoryRequirements)
		}
		if seen[key] {
			return fmt.Errorf("%w: duplicate directory key %q", ErrInvalidDirectoryRequirements, key)
		}
		seen[key] = true
		if strings.TrimSpace(req.Label) == "" {
			return fmt.Errorf("%w: directory %q has a blank label", ErrInvalidDirectoryRequirements, key)
		}
		if err := validateSuggestedPath(req.SuggestedPath); err != nil {
			return fmt.Errorf("%w: directory %q %v", ErrInvalidDirectoryRequirements, key, err)
		}
	}
	return nil
}

// validateSuggestedPath rejects a suggested path that could not be a plain
// folder hint: parent-relative segments and embedded NULs. The path is a
// suggestion for a picker, so it is never resolved here — this only keeps
// obviously hostile values out of the manifest.
func validateSuggestedPath(p string) error {
	p = strings.TrimSpace(p)
	if p == "" {
		return nil
	}
	if strings.ContainsRune(p, 0) {
		return errors.New("has a suggested path containing a NUL byte")
	}
	// Check the raw segments, not the cleaned path: "~/Downloads/../../etc"
	// cleans to "etc" and would otherwise look innocent.
	if slices.Contains(strings.Split(strings.ReplaceAll(p, "\\", "/"), "/"), "..") {
		return fmt.Errorf("has a suggested path with a %q segment: %q", "..", p)
	}
	return nil
}

// normalizeDirectoryRequirements trims/lower-cases keys, drops requirements
// with a blank key, falls back to the key for a blank label, and keeps the
// first of any duplicate keys rather than failing — matching
// normalizeCapabilityRequirements' load-time leniency.
func normalizeDirectoryRequirements(reqs []DirectoryRequirement) []DirectoryRequirement {
	if len(reqs) == 0 {
		return nil
	}
	out := make([]DirectoryRequirement, 0, len(reqs))
	seen := make(map[string]bool, len(reqs))
	for _, req := range reqs {
		key := strings.ToLower(strings.TrimSpace(req.Key))
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		label := strings.TrimSpace(req.Label)
		if label == "" {
			label = key
		}
		suggested := strings.TrimSpace(req.SuggestedPath)
		if validateSuggestedPath(suggested) != nil {
			suggested = ""
		}
		out = append(out, DirectoryRequirement{
			Key:              key,
			Label:            label,
			SuggestedPath:    suggested,
			AccessDisclosure: strings.TrimSpace(req.AccessDisclosure),
		})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// validateAutomationRecipes enforces authoring-save invariants on a raw
// (pre-normalization) edit, checked against the directory requirements the same
// save declares: every recipe must name a declared directory exactly once,
// request at least one form of automation, and carry in-range values.
func validateAutomationRecipes(recipes []AutomationRecipe, reqs []DirectoryRequirement) error {
	declared := make(map[string]bool, len(reqs))
	for _, req := range reqs {
		declared[strings.ToLower(strings.TrimSpace(req.Key))] = true
	}
	seen := make(map[string]bool, len(recipes))
	for _, recipe := range recipes {
		key := strings.ToLower(strings.TrimSpace(recipe.DirectoryKey))
		if key == "" {
			return fmt.Errorf("%w: automation recipe is missing directory_key", ErrInvalidAutomationRecipes)
		}
		if !declared[key] {
			return fmt.Errorf("%w: recipe references undeclared directory key %q", ErrInvalidAutomationRecipes, key)
		}
		if seen[key] {
			return fmt.Errorf("%w: duplicate recipe for directory key %q", ErrInvalidAutomationRecipes, key)
		}
		seen[key] = true
		if recipe.Watch == nil && recipe.DailyScan == nil {
			return fmt.Errorf("%w: recipe for %q declares neither watch nor daily_scan", ErrInvalidAutomationRecipes, key)
		}
		if err := validateWatchRecipe(key, recipe.Watch); err != nil {
			return err
		}
		if err := validateDailyScanRecipe(key, recipe.DailyScan); err != nil {
			return err
		}
	}
	return nil
}

func validateWatchRecipe(key string, watch *WatchRecipe) error {
	if watch == nil {
		return nil
	}
	for _, event := range watch.Events {
		normalized := strings.ToLower(strings.TrimSpace(event))
		if normalized == "" {
			return fmt.Errorf("%w: recipe for %q has a blank watch event", ErrInvalidAutomationRecipes, key)
		}
		if !slices.Contains(ValidRecipeFileEvents, normalized) {
			return fmt.Errorf("%w: recipe for %q has unknown watch event %q", ErrInvalidAutomationRecipes, key, event)
		}
	}
	if watch.DebounceSeconds < 0 || watch.DebounceSeconds > maxRecipeDebounceSeconds {
		return fmt.Errorf("%w: recipe for %q has debounce_seconds %d outside 0-%d", ErrInvalidAutomationRecipes, key, watch.DebounceSeconds, maxRecipeDebounceSeconds)
	}
	for _, dir := range watch.ExcludeSubdirectories {
		name := strings.TrimSpace(dir)
		if name == "" {
			return fmt.Errorf("%w: recipe for %q has a blank excluded subdirectory", ErrInvalidAutomationRecipes, key)
		}
		if name != path.Base(name) || name == "." || name == ".." || strings.ContainsAny(name, `/\`) {
			return fmt.Errorf("%w: recipe for %q excludes %q, which is not an immediate child directory name", ErrInvalidAutomationRecipes, key, dir)
		}
	}
	return nil
}

func validateDailyScanRecipe(key string, daily *DailyScanRecipe) error {
	if daily == nil {
		return nil
	}
	if _, err := workspace.NormalizeLocalTimeOfDay(daily.LocalTime); err != nil {
		return fmt.Errorf("%w: recipe for %q %v", ErrInvalidAutomationRecipes, key, err)
	}
	if tz := strings.TrimSpace(daily.Timezone); tz != "" {
		if _, err := time.LoadLocation(tz); err != nil {
			return fmt.Errorf("%w: recipe for %q has unknown timezone %q", ErrInvalidAutomationRecipes, key, daily.Timezone)
		}
	}
	return nil
}

// normalizeAutomationRecipes drops recipes with a blank/duplicate directory
// key, drops recipes that request no automation, and drops individual watch or
// daily-scan blocks whose values are unusable — load-time leniency, so a
// hand-edited manifest degrades to less automation instead of failing to load.
// declaredKeys, when non-empty, additionally drops recipes for directories the
// template does not declare.
func normalizeAutomationRecipes(recipes []AutomationRecipe, reqs []DirectoryRequirement) []AutomationRecipe {
	if len(recipes) == 0 {
		return nil
	}
	declared := make(map[string]bool, len(reqs))
	for _, req := range reqs {
		declared[strings.ToLower(strings.TrimSpace(req.Key))] = true
	}
	out := make([]AutomationRecipe, 0, len(recipes))
	seen := make(map[string]bool, len(recipes))
	for _, recipe := range recipes {
		key := strings.ToLower(strings.TrimSpace(recipe.DirectoryKey))
		if key == "" || seen[key] || !declared[key] {
			continue
		}
		normalized := AutomationRecipe{
			DirectoryKey: key,
			Watch:        normalizeWatchRecipe(key, recipe.Watch),
			DailyScan:    normalizeDailyScanRecipe(key, recipe.DailyScan),
		}
		if normalized.Watch == nil && normalized.DailyScan == nil {
			continue
		}
		seen[key] = true
		out = append(out, normalized)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func normalizeWatchRecipe(key string, watch *WatchRecipe) *WatchRecipe {
	if watch == nil || validateWatchRecipe(key, watch) != nil {
		return nil
	}
	normalized := WatchRecipe{DebounceSeconds: watch.DebounceSeconds}
	normalized.Events = normalizeOperationNames(watch.Events)
	for _, dir := range watch.ExcludeSubdirectories {
		normalized.ExcludeSubdirectories = append(normalized.ExcludeSubdirectories, strings.TrimSpace(dir))
	}
	return &normalized
}

func normalizeDailyScanRecipe(key string, daily *DailyScanRecipe) *DailyScanRecipe {
	if daily == nil || validateDailyScanRecipe(key, daily) != nil {
		return nil
	}
	localTime, err := workspace.NormalizeLocalTimeOfDay(daily.LocalTime)
	if err != nil {
		return nil
	}
	return &DailyScanRecipe{LocalTime: localTime, Timezone: strings.TrimSpace(daily.Timezone)}
}

// CapabilityRequirement returns the capability the template declares under the
// given key, if any.
func (t Template) CapabilityRequirement(key string) (CapabilityRequirement, bool) {
	key = strings.ToLower(strings.TrimSpace(key))
	for _, req := range t.CapabilityRequirements {
		if req.Key == key {
			return req, true
		}
	}
	return CapabilityRequirement{}, false
}

// DirectoryRequirement returns the requirement with the given key, if declared.
func (t Template) DirectoryRequirement(key string) (DirectoryRequirement, bool) {
	key = strings.ToLower(strings.TrimSpace(key))
	for _, req := range t.DirectoryRequirements {
		if req.Key == key {
			return req, true
		}
	}
	return DirectoryRequirement{}, false
}

// AutomationRecipeFor returns the recipe automating the given directory key, if
// any.
func (t Template) AutomationRecipeFor(key string) (AutomationRecipe, bool) {
	key = strings.ToLower(strings.TrimSpace(key))
	for _, recipe := range t.AutomationRecipes {
		if recipe.DirectoryKey == key {
			return recipe, true
		}
	}
	return AutomationRecipe{}, false
}
