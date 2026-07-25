// Package projecttemplates implements domain-blind project templates: folder
// skeletons that are copied into a workspace folder to become its project
// (referenced by the workspace's project_path). The package copies bytes and
// substitutes tokens in file names; it never interprets file contents, so all
// domain specificity lives in template data, not code.
package projecttemplates

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// ManifestFileName is the optional per-template metadata file. It carries
// constrained declarative metadata, never executable behavior, and is excluded
// from instantiation.
const ManifestFileName = "template.json"

// Behavior profiles understood by the workspace-creation flow (sent as
// workspace_preset). Keep in sync with internal/workspacesettings and the
// create-modal "Agent behavior" control.
const (
	BehaviorProfileGeneral         = "general"
	BehaviorProfileResearch        = "research"
	BehaviorProfileSoftwareProject = "software_project"
)

// ValidBehaviorProfiles is the accepted set of behavior profiles.
var ValidBehaviorProfiles = []string{
	BehaviorProfileGeneral,
	BehaviorProfileResearch,
	BehaviorProfileSoftwareProject,
}

// NormalizeBehaviorProfile lowercases and validates a behavior profile,
// falling back to "general" when empty or unrecognized so a bad manifest value
// never breaks listing or workspace creation.
func NormalizeBehaviorProfile(p string) string {
	p = strings.ToLower(strings.TrimSpace(p))
	if slices.Contains(ValidBehaviorProfiles, p) {
		return p
	}
	return BehaviorProfileGeneral
}

// StarterTask is one example task a template seeds into a new workspace. It is
// data only — seeding happens in the create flow, not this package.
type StarterTask struct {
	Description string `json:"description"`
	Details     string `json:"details,omitempty"`
	// Setup marks the template's setup task: seeded like every other starter
	// task, but auto-started once when the user first opens the workspace. At
	// most one task per template may set it — the authoring save path rejects
	// extras, and load-time normalization keeps only the first flag from a
	// hand-edited manifest.
	Setup bool `json:"setup,omitempty"`
}

// ErrInvalidStarterTasks reports a starter-task edit that violates the setup
// rules (more than one task flagged `setup: true`).
var ErrInvalidStarterTasks = errors.New("invalid starter tasks")

// ErrInvalidCapabilityRequirements reports a capability-requirement edit with
// a blank key, a blank operation name, or a duplicate key.
var ErrInvalidCapabilityRequirements = errors.New("invalid capability requirements")

// CapabilityRequirement declares an abstract capability (e.g. "calendar")
// this template needs, without naming a specific MCP server, skill, or
// plugin. Workspace creation carries an unresolved requirement into setup
// readiness rather than auto-installing or silently choosing a connector —
// the user picks or connects one during guided setup. Key and operation
// names are always normalized (trimmed, lower-cased); see
// normalizeCapabilityRequirements / validateCapabilityRequirements.
type CapabilityRequirement struct {
	// Key identifies the capability (e.g. "calendar"). Consuming code (e.g.
	// internal/calendar for "calendar") defines what the key means and which
	// operation names are valid; this package only stores and normalizes the
	// data, staying domain-blind like the rest of the template system.
	Key string `json:"key"`
	// RequiredOperations must be mapped before the capability is ready.
	RequiredOperations []string `json:"required_operations,omitempty"`
	// OptionalOperations may be mapped; the corresponding UI action only
	// appears when they are.
	OptionalOperations []string `json:"optional_operations,omitempty"`
}

// validateCapabilityRequirements enforces authoring-save invariants on a raw
// (pre-normalization) edit: every key must be non-blank and unique, and every
// operation name must be non-blank. Mirrors validateStarterTasks: normalize
// functions are lenient (self-healing on load), while the authoring save path
// returns an error instead of silently dropping bad data.
func validateCapabilityRequirements(reqs []CapabilityRequirement) error {
	seen := make(map[string]bool, len(reqs))
	for _, req := range reqs {
		key := strings.ToLower(strings.TrimSpace(req.Key))
		if key == "" {
			return fmt.Errorf("%w: capability key is required", ErrInvalidCapabilityRequirements)
		}
		if seen[key] {
			return fmt.Errorf("%w: duplicate capability key %q", ErrInvalidCapabilityRequirements, key)
		}
		seen[key] = true
		for _, op := range append(append([]string{}, req.RequiredOperations...), req.OptionalOperations...) {
			if strings.TrimSpace(op) == "" {
				return fmt.Errorf("%w: capability %q has a blank operation name", ErrInvalidCapabilityRequirements, key)
			}
		}
	}
	return nil
}

// normalizeCapabilityRequirements trims/lower-cases keys and operation names,
// drops requirements with a blank key, drops blank operation names, and
// merges duplicate keys (first-seen wins, later operations unioned in) rather
// than failing — matching normalizeStarterTasks' load-time leniency.
func normalizeCapabilityRequirements(reqs []CapabilityRequirement) []CapabilityRequirement {
	if len(reqs) == 0 {
		return nil
	}
	out := make([]CapabilityRequirement, 0, len(reqs))
	index := make(map[string]int, len(reqs))
	for _, req := range reqs {
		key := strings.ToLower(strings.TrimSpace(req.Key))
		if key == "" {
			continue
		}
		required := normalizeOperationNames(req.RequiredOperations)
		optional := normalizeOperationNames(req.OptionalOperations)
		if pos, exists := index[key]; exists {
			out[pos].RequiredOperations = mergeUniqueStrings(out[pos].RequiredOperations, required)
			out[pos].OptionalOperations = mergeUniqueStrings(out[pos].OptionalOperations, optional)
			continue
		}
		index[key] = len(out)
		out = append(out, CapabilityRequirement{Key: key, RequiredOperations: required, OptionalOperations: optional})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func normalizeOperationNames(ops []string) []string {
	if len(ops) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(ops))
	out := make([]string, 0, len(ops))
	for _, op := range ops {
		op = strings.ToLower(strings.TrimSpace(op))
		if op == "" {
			continue
		}
		if _, exists := seen[op]; exists {
			continue
		}
		seen[op] = struct{}{}
		out = append(out, op)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func mergeUniqueStrings(a, b []string) []string {
	seen := make(map[string]struct{}, len(a)+len(b))
	out := make([]string, 0, len(a)+len(b))
	for _, v := range a {
		if _, exists := seen[v]; exists {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	for _, v := range b {
		if _, exists := seen[v]; exists {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// validateStarterTasks enforces the at-most-one setup task rule on a raw
// (pre-normalization) edit, naming the offending tasks. Normalization silently
// demotes extra flags for resilience on load; this save-path check exists so an
// author gets an error instead of a silent demotion.
func validateStarterTasks(tasks []StarterTask) error {
	var setupTasks []string
	for _, task := range tasks {
		if !task.Setup {
			continue
		}
		desc := strings.TrimSpace(task.Description)
		if desc == "" {
			desc = "(no description)"
		}
		setupTasks = append(setupTasks, fmt.Sprintf("%q", desc))
	}
	if len(setupTasks) > 1 {
		return fmt.Errorf("%w: at most one starter task may set setup: true, got %d (%s)", ErrInvalidStarterTasks, len(setupTasks), strings.Join(setupTasks, ", "))
	}
	return nil
}

// Template describes one instantiable folder skeleton (or, when HasSkeleton is
// false, a metadata-only template that contributes name/behavior/tasks/tools
// but creates no project folder).
type Template struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	// Icon is an optional emoji glyph shown on the create-modal picker card.
	Icon string `json:"icon,omitempty"`
	// Tagline is a one-line summary shown on the compact create-modal picker
	// card. Falls back to Description (truncated) when absent.
	Tagline string `json:"tagline,omitempty"`
	// Addons are short recommended-addon labels (e.g. "Citations skill") shown
	// as chips in the create-modal briefing panel's "deploys" readout.
	Addons []string `json:"addons,omitempty"`
	// BehaviorProfile is the workspace behavior profile (workspace_preset) a
	// workspace created from this template defaults to. Always normalized to one
	// of ValidBehaviorProfiles.
	BehaviorProfile string `json:"behavior_profile,omitempty"`
	// StarterTasks are example tasks seeded into a new workspace.
	StarterTasks []StarterTask `json:"starter_tasks,omitempty"`
	// ProjectEntry is the optional scaffolded file Ori can offer to open after
	// an explicit Create Workspace action. It is validated data only.
	ProjectEntry *ProjectEntry `json:"project_entry,omitempty"`
	// Builtin marks a template shipped with the app: read-only in the authoring
	// UI and grouped as a built-in in the create-modal picker.
	Builtin bool `json:"builtin"`
	// BuiltinVersion is the shipped revision of a built-in's manifest. When the
	// embedded version exceeds the on-disk copy, EnsureLibrary refreshes the
	// built-in's template.json so metadata changes (rosters, tags, …) reach
	// existing installs. Zero/absent for user templates.
	BuiltinVersion int `json:"builtin_version,omitempty"`
	// HasSkeleton reports whether the template has instantiable files beyond
	// template.json. A metadata-only template (false) creates no project folder.
	HasSkeleton bool `json:"has_skeleton"`
	// Path is the template folder's absolute path on disk.
	Path string `json:"-"`
	// Onboarding is the verbatim legacy `onboarding` block from template.json,
	// if any. The intake engine that executed it has been removed: the bytes are
	// carried only so warnings can flag the block and the authoring save path
	// can strip it. Never parsed or executed. Excluded from API JSON.
	Onboarding json.RawMessage `json:"-"`
	// Tools are the default skills/MCP servers/plugins a workspace created from
	// this template binds (apply-if-present). Names only — bound in the
	// workspace-creation layer, not here.
	Tools ToolDefaults `json:"tools"`
	// Agents is the ordered set of reusable agents a workspace attaches from
	// this template. The first becomes that workspace's primary routing agent;
	// the rest are workspace specialists. Carried as data only; global agent
	// definitions and workspace attachments are resolved in creation (see AgentSpec).
	Agents []AgentSpec `json:"agents,omitempty"`
	// Warnings are non-fatal authoring problems computed at load time (legacy
	// onboarding block, missing agent roster). They surface through the list
	// API so the /templates UI can flag them; they never block loading or
	// instantiation.
	Warnings []string `json:"warnings,omitempty"`
	// CapabilityRequirements are the abstract capabilities (e.g. "calendar")
	// this template needs. Carried as data only, same as Tools/Agents — the
	// workspace-creation layer resolves them against connected MCP servers.
	CapabilityRequirements []CapabilityRequirement `json:"capability_requirements,omitempty"`
	// DirectoryRequirements are the local folders this template asks the user to
	// select during guided setup. Data only: nothing is expanded, resolved, or
	// created here — see setup_requirements.go.
	DirectoryRequirements []DirectoryRequirement `json:"directory_requirements,omitempty"`
	// AutomationRecipes are the watchers/daily runs to install once the matching
	// directory requirement has been confirmed. Inert until setup completes.
	AutomationRecipes []AutomationRecipe `json:"automation_recipes,omitempty"`
}

// HasOnboarding reports whether the template still carries a legacy intake-era
// onboarding block (detection only — the block is ignored at runtime and
// stripped on the next authoring save).
func (t Template) HasOnboarding() bool {
	s := bytes.TrimSpace(t.Onboarding)
	return len(s) > 0 && !bytes.Equal(s, []byte("null"))
}

// HasAgents reports whether the template declares at least one agent to seed.
func (t Template) HasAgents() bool {
	return len(t.Agents) > 0
}

// manifest is the on-disk shape of template.json. Unknown fields are ignored
// by design. Display fields (name/description/tags) are metadata only; a
// legacy `onboarding` block is preserved verbatim as raw JSON purely for
// warning detection and strip-on-save — never interpreted, so the file-copy
// engine stays domain-blind.
type manifest struct {
	Name                   string                  `json:"name"`
	Description            string                  `json:"description"`
	Tags                   []string                `json:"tags,omitempty"`
	Icon                   string                  `json:"icon,omitempty"`
	Tagline                string                  `json:"tagline,omitempty"`
	Addons                 []string                `json:"addons,omitempty"`
	BehaviorProfile        string                  `json:"behavior_profile,omitempty"`
	StarterTasks           []StarterTask           `json:"starter_tasks,omitempty"`
	ProjectEntry           json.RawMessage         `json:"project_entry,omitempty"`
	Builtin                bool                    `json:"builtin,omitempty"`
	BuiltinVersion         int                     `json:"builtin_version,omitempty"`
	Onboarding             json.RawMessage         `json:"onboarding,omitempty"`
	Tools                  *ToolDefaults           `json:"tools,omitempty"`
	Agents                 []AgentSpec             `json:"agents,omitempty"`
	CapabilityRequirements []CapabilityRequirement `json:"capability_requirements,omitempty"`
	DirectoryRequirements  []DirectoryRequirement  `json:"directory_requirements,omitempty"`
	AutomationRecipes      []AutomationRecipe      `json:"automation_recipes,omitempty"`
}

// readManifest loads template.json from dir. A missing or malformed manifest
// is not an error — the template simply falls back to folder-name display.
// dir is either a library template folder or a folder the caller (an
// admin-facing, local-first tool) explicitly chose via LoadFolder; the
// filename is always the fixed ManifestFileName constant.
func readManifest(dir string) manifest {
	data, err := os.ReadFile(filepath.Join(dir, ManifestFileName)) // #nosec G304 -- dir is a library/template folder resolved by the caller; filename is the fixed ManifestFileName constant
	if err != nil {
		return manifest{}
	}
	var m manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return manifest{}
	}
	return m
}

// newTemplate builds a Template for the folder at path, applying manifest
// display overrides when present.
func newTemplate(path string) Template {
	t := Template{
		ID:   filepath.Base(filepath.Clean(path)),
		Path: filepath.Clean(path),
	}
	m := readManifest(t.Path)
	t.Name = strings.TrimSpace(m.Name)
	if t.Name == "" {
		t.Name = t.ID
	}
	t.Description = strings.TrimSpace(m.Description)
	t.Tags = workspace.NormalizeWorkspaceTags(m.Tags)
	t.Icon = strings.TrimSpace(m.Icon)
	t.Tagline = strings.TrimSpace(m.Tagline)
	t.Addons = normalizeAddons(m.Addons)
	t.BehaviorProfile = NormalizeBehaviorProfile(m.BehaviorProfile)
	t.StarterTasks = normalizeStarterTasks(m.StarterTasks)
	t.Builtin = m.Builtin || IsBuiltinStarterID(t.ID)
	t.BuiltinVersion = m.BuiltinVersion
	t.HasSkeleton = hasSkeletonFiles(t.Path)
	t.Onboarding = m.Onboarding
	projectEntry, projectEntryErr := normalizeManifestProjectEntry(t.Path, m.ProjectEntry)
	t.ProjectEntry = projectEntry
	if m.Tools != nil {
		t.Tools = normalizeToolDefaults(*m.Tools)
	}
	t.Agents = normalizeAgentSpecs(m.Agents)
	t.CapabilityRequirements = normalizeCapabilityRequirements(m.CapabilityRequirements)
	t.DirectoryRequirements = normalizeDirectoryRequirements(m.DirectoryRequirements)
	t.AutomationRecipes = normalizeAutomationRecipes(m.AutomationRecipes, t.DirectoryRequirements)
	t.Warnings = manifestWarnings(m, t.Agents)
	if projectEntryErr != nil {
		t.Warnings = append(t.Warnings, fmt.Sprintf("template.json project_entry is ignored: %v", projectEntryErr))
	}
	return t
}

// manifestWarnings reports non-fatal authoring problems for a loaded template:
// a legacy intake-era `onboarding` block (ignored at runtime, stripped on the
// next authoring save) and a missing agent roster (every template should
// declare one — workspace creation falls back to auto-creating a
// "<Workspace Name> Manager" entry agent when it is absent).
func manifestWarnings(m manifest, agents []AgentSpec) []string {
	var warnings []string
	if s := bytes.TrimSpace(m.Onboarding); len(s) > 0 && !bytes.Equal(s, []byte("null")) {
		warnings = append(warnings, `template.json contains a legacy "onboarding" block; it is ignored and will be removed the next time the template is saved`)
	}
	if len(agents) == 0 {
		warnings = append(warnings, "template declares no agents; add a roster (the first agent is the entry agent) — until then, workspaces created from it get an auto-created manager agent")
	}
	return warnings
}

// normalizeStarterTasks trims each task and drops any without a description.
// The setup flag survives normalization, but only on the first flagged task —
// later flags from a hand-edited manifest are demoted so loading never fails
// and downstream seeding sees at most one setup task.
func normalizeStarterTasks(tasks []StarterTask) []StarterTask {
	if len(tasks) == 0 {
		return nil
	}
	out := make([]StarterTask, 0, len(tasks))
	haveSetup := false
	for _, task := range tasks {
		desc := strings.TrimSpace(task.Description)
		if desc == "" {
			continue
		}
		setup := task.Setup && !haveSetup
		haveSetup = haveSetup || setup
		out = append(out, StarterTask{Description: desc, Details: strings.TrimSpace(task.Details), Setup: setup})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// normalizeAddons trims each recommended-addon label and drops empties.
func normalizeAddons(addons []string) []string {
	if len(addons) == 0 {
		return nil
	}
	out := make([]string, 0, len(addons))
	for _, addon := range addons {
		addon = strings.TrimSpace(addon)
		if addon == "" {
			continue
		}
		out = append(out, addon)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// hasSkeletonFiles reports whether dir holds any instantiable entry beyond the
// manifest (template.json). A metadata-only template contains just the
// manifest. A read error is treated as "no skeleton" so the template still
// lists as metadata-only rather than failing.
func hasSkeletonFiles(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if entry.Name() == ManifestFileName {
			continue
		}
		return true
	}
	return false
}

// ListLibrary returns the templates in the library directory: every immediate
// subfolder is one template, identified by its folder name. Hidden folders
// (dot-prefixed) are skipped. A missing library directory yields an empty
// list, not an error, so a fresh install works before anything is authored.
func ListLibrary(dir string) ([]Template, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return nil, nil
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read templates directory %s: %w", dir, err)
	}

	templates := make([]Template, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		templates = append(templates, newTemplate(filepath.Join(dir, entry.Name())))
	}

	sort.Slice(templates, func(i, j int) bool { return templates[i].ID < templates[j].ID })
	return templates, nil
}

// FindLibraryTemplate resolves a template ID against the library directory.
func FindLibraryTemplate(dir, id string) (Template, error) {
	id = strings.TrimSpace(id)
	if id == "" || id != filepath.Base(id) || strings.HasPrefix(id, ".") {
		return Template{}, fmt.Errorf("%w: %q", ErrTemplateNotFound, id)
	}

	path := filepath.Join(dir, id)
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return Template{}, fmt.Errorf("%w: %q", ErrTemplateNotFound, id)
	}
	return newTemplate(path), nil
}

// LoadFolder treats an arbitrary folder on disk as a template (the
// "Choose folder…" escape hatch). It is handled identically to a library
// template — including the optional manifest — but is not part of the library.
func LoadFolder(path string) (Template, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return Template{}, fmt.Errorf("%w: empty path", ErrTemplateNotFound)
	}

	abs, err := filepath.Abs(path)
	if err != nil {
		return Template{}, fmt.Errorf("%w: %q", ErrTemplateNotFound, path)
	}
	info, err := os.Stat(abs)
	if err != nil || !info.IsDir() {
		return Template{}, fmt.Errorf("%w: %q is not a folder", ErrTemplateNotFound, path)
	}
	return newTemplate(abs), nil
}
