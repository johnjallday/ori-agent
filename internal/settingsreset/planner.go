package settingsreset

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/johnjallday/ori-agent/internal/config"
	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/onboarding"
	"github.com/johnjallday/ori-agent/internal/resetstate"
	"github.com/johnjallday/ori-agent/internal/sessionfiles"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/vault"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

const (
	previewLifetime = 10 * time.Minute
	maxPreviews     = 32
	maxPlanBytes    = 64 * 1024
)

var (
	ErrPreviewExpired = errors.New("reset preview expired or unknown; review the scope again")
	ErrScopeChanged   = errors.New("reset scope changed; review a new preview")
	ErrPreviewBlocked = errors.New("reset preview has unresolved safety blockers")
	ErrPreviewLimit   = errors.New("reset preview capacity or size limit reached")
)

// Owners are supplied only by runtime construction, never decoded from HTTP.
// Nil owners produce blockers/unknown facts instead of reconstructed defaults.
// CheckLifecycle must inspect ownership/admission only, without stopping work.
type Owners struct {
	DataDir        string
	Config         *config.Manager
	Agents         store.Store
	Setup          *onboarding.Manager
	Database       *database.DB
	Uploads        *sessionfiles.Store
	Workspaces     *workspace.FileStore
	Allowlist      *workspace.Allowlist
	Vaults         *vault.Store
	CheckLifecycle func(context.Context) []Blocker
}

// Planner holds bounded, expiring, server-owned confirmations. It has no apply,
// close, migration, credential read/write, or destructive filesystem capability.
// Validation here is necessary but not sufficient for operation admission.
type Planner struct {
	owners func() Owners
	now    func() time.Time
	mu     sync.Mutex
	plans  map[string]resolvedPlan
}

type resolvedTarget struct {
	Category CategoryID `json:"category"`
	Kind     string     `json:"kind"` // owner-specific operation, never a recursive root wipe
	Path     string     `json:"path"`
}

type protectedDigest struct {
	Path   string `json:"path"`
	Digest string `json:"digest"`
}

type resolvedEvidence struct {
	ProtectedPaths         []string          `json:"protected_paths"`
	ProtectedDigests       []protectedDigest `json:"protected_digests"`
	DatabasePath           string            `json:"database_path"`
	DatabaseSchema         string            `json:"database_schema"`
	DatabaseSchemaVersion  int               `json:"database_schema_version"`
	WorkspaceRootConfirmed bool              `json:"workspace_root_confirmed"`
}

type resolvedPlan struct {
	Preview      Preview          `json:"preview"`
	Targets      []resolvedTarget `json:"targets"`
	Installation string           `json:"installation"`
	Evidence     resolvedEvidence `json:"evidence"`
}

func NewPlanner(owners func() Owners) *Planner {
	return &Planner{owners: owners, now: time.Now, plans: make(map[string]resolvedPlan)}
}

func (p *Planner) Create(ctx context.Context, intent Intent, categories []CategoryID) (Preview, error) {
	selected, err := Selection(intent, categories)
	if err != nil {
		return Preview{}, err
	}
	plan, err := p.inspect(ctx, intent, selected)
	if err != nil {
		return Preview{}, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	now := p.now()
	for id, saved := range p.plans {
		if !now.Before(saved.Preview.ExpiresAt) {
			delete(p.plans, id)
		}
	}
	if len(p.plans) >= maxPreviews {
		return Preview{}, ErrPreviewLimit
	}
	plan.Preview.ID = rand.Text()
	plan.Preview.OperationID = rand.Text()
	plan.Preview.ExpiresAt = now.Add(previewLifetime).UTC()
	encoded, err := json.Marshal(plan)
	if err != nil || len(encoded) > maxPlanBytes {
		return Preview{}, ErrPreviewLimit
	}
	p.plans[plan.Preview.ID] = plan
	return clonePreview(plan.Preview), nil
}

// Validate re-inspects trusted owners. Counts may change; target, schema,
// preservation, dependency or readiness changes require a new review. This
// does not consume the preview or perform operation admission (the coordinator
// must serialize admission and record consumption durably before effects).
func (p *Planner) Validate(ctx context.Context, id string) (Preview, error) {
	p.mu.Lock()
	saved, ok := p.plans[id]
	expired := !ok || !p.now().Before(saved.Preview.ExpiresAt)
	p.mu.Unlock()
	if expired {
		return Preview{}, ErrPreviewExpired
	}
	current, err := p.inspect(ctx, saved.Preview.Intent, saved.Preview.Selected)
	if err != nil {
		return Preview{}, err
	}
	p.mu.Lock()
	expired = !p.now().Before(saved.Preview.ExpiresAt)
	p.mu.Unlock()
	if expired {
		return Preview{}, ErrPreviewExpired
	}
	if current.Preview.ScopeDigest != saved.Preview.ScopeDigest {
		return Preview{}, ErrScopeChanged
	}
	if len(current.Preview.Blockers) != 0 {
		return clonePreview(current.Preview), ErrPreviewBlocked
	}
	current.Preview.ID, current.Preview.ExpiresAt = saved.Preview.ID, saved.Preview.ExpiresAt
	current.Preview.OperationID = saved.Preview.OperationID
	return clonePreview(current.Preview), nil
}

// validatedPlan is private: HTTP never supplies or receives executable targets.
func (p *Planner) validatedPlan(ctx context.Context, id string) (resolvedPlan, error) {
	view, err := p.Validate(ctx, id)
	if err != nil {
		return resolvedPlan{}, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	saved, ok := p.plans[id]
	if !ok {
		return resolvedPlan{}, ErrPreviewExpired
	}
	saved.Preview = view
	saved.Targets = slices.Clone(saved.Targets)
	return saved, nil
}

func clonePreview(preview Preview) Preview {
	// All members are plain JSON payload types; serialization cannot fail.
	data, _ := json.Marshal(preview)
	var cloned Preview
	_ = json.Unmarshal(data, &cloned)
	return cloned
}

func (p *Planner) inspect(ctx context.Context, intent Intent, selected []CategoryID) (resolvedPlan, error) {
	if err := ctx.Err(); err != nil {
		return resolvedPlan{}, err
	}
	owners := Owners{}
	if p.owners != nil {
		owners = p.owners()
	}
	plan := resolvedPlan{Preview: Preview{
		SchemaVersion: SchemaVersion, Intent: intent, Selected: slices.Clone(selected),
		Dependencies: []CategoryID{}, Categories: []CategoryPreview{}, Blockers: []Blocker{},
		Restart: RestartInfo{Mode: RestartProcessRelaunch, Instructions: "Fully stop and relaunch Ori using the same installation. In menubar, Quit the application; Stop/Start Server is insufficient."},
	}}
	view := &plan.Preview
	block := func(code string, category CategoryID, message, recovery string) {
		item := Blocker{Code: code, Category: category, Message: message, Recovery: recovery}
		if !slices.Contains(view.Blockers, item) {
			view.Blockers = append(view.Blockers, item)
		}
	}
	if owners.CheckLifecycle == nil {
		block("lifecycle_unavailable", "", "This host has not established safe reset ownership and restart admission.", "Use a host with the staged pre-start reset lifecycle; do not delete live database files.")
	} else {
		for _, item := range owners.CheckLifecycle(ctx) {
			block(item.Code, item.Category, item.Message, item.Recovery)
		}
	}
	root, err := resolvePath(owners.DataDir)
	if err != nil || filepath.Dir(root) == root {
		block("installation_unavailable", "", "The installation root is missing, unsafe or unreadable.", "Resolve the runtime data directory before reviewing reset.")
		root = ""
	}
	plan.Installation = root
	var protected []string
	retain := func(path string) {
		resolved, err := resolvePath(path)
		if err != nil || len(resolved) > 4096 {
			block("retained_path_unavailable", "", "A retained location cannot be safely resolved.", "Restore access to the configured workspace/vault location, then review again.")
			return
		}
		protected = append(protected, resolved)
	}
	if owners.Workspaces != nil {
		retain(owners.Workspaces.BasePath())
	} else {
		block("workspace_owner_unavailable", "", "The authoritative workspace root is unavailable.", "Restore the workspace owner before reset so nested project files can be protected.")
	}
	if owners.Config != nil {
		for _, path := range []string{owners.Config.GetWorkspaceRoot(), owners.Config.GetVaultRoot()} {
			if path != "" {
				retain(path)
			}
		}
	}
	if owners.Vaults != nil {
		retain(owners.Vaults.ManagedRoot())
	}
	var report database.ResetInspection
	databasePath := ""
	rootConfirmed := owners.Config != nil && owners.Config.IsWorkspaceRootConfirmed()
	if owners.Database == nil {
		block("database_owner_unavailable", "", "Database and retained vault catalog inspection are unavailable.", "Preserve the database and WAL files. Restore the authoritative owner; if startup reported legacy vault data, recover/export it with a compatible Ori version. Unavailable records are not an empty installation.")
	} else {
		databasePath, err = resolvePath(owners.Database.Path())
		if err != nil {
			block("database_location_unavailable", "", "The authoritative database location is unavailable.", "Use an owned on-disk database before destructive reset.")
		}
		report, err = database.InspectReset(ctx, owners.Database.DB)
		if err != nil {
			block("database_inspection_unavailable", "", "The existing database could not be inspected without migration.", "Restore read access and retry preview. Preserve the database and its WAL files.")
		}
		for _, problem := range report.Problems {
			block(problem, "", "Database schema or retained vault evidence prevents safe reset.", "Preserve the database. Recover/export legacy vault data with a compatible version; resolve unknown or unreadable domains before reset.")
		}
		for _, path := range report.VaultPaths {
			if owners.Vaults == nil {
				block("vault_owner_unavailable", "", "Catalog paths cannot be resolved by the vault owner.", "Restore the vault owner before reset; do not detach unresolved vaults.")
				break
			}
			retain(owners.Vaults.RetainedPath(path))
		}
	}
	slices.Sort(protected)
	protected = slices.Compact(protected)
	protectedDigests := make([]protectedDigest, 0, len(protected))
	for _, path := range protected {
		digest, err := digestProtectedPath(ctx, path)
		if err != nil {
			block("retained_path_unreadable", selected[0], "Retained workspace or vault contents cannot be hashed before reset.", "Restore read access or reduce the retained tree below the bounded inspection limit, then review again.")
		}
		protectedDigests = append(protectedDigests, protectedDigest{Path: path, Digest: digest})
	}
	for _, id := range selected {
		def, _ := definition(id)
		category := CategoryPreview{ID: id, Label: def.Label, Description: def.Description, Facts: []CountFact{}, Removed: []Location{}, Retained: []Location{}}
		for _, path := range protected {
			category.Retained = append(category.Retained, Location{DisplayPath: path, Reason: "Retain workspace/project or vault backing files, including nested contents and encryption material."})
		}
		category.Retained = append(category.Retained, Location{DisplayPath: "Environment, external CLI authentication and third-party accounts", Reason: "Not removed or revoked. External credentials may still make a provider available."})
		target := func(kind, path, reason string) {
			resolved, err := resolvePath(path)
			if err != nil || len(resolved) > 4096 {
				block("target_unavailable", id, "An authoritative reset target cannot be resolved.", "Restore access to that owner and review again.")
				return
			}
			category.Removed = append(category.Removed, Location{DisplayPath: resolved, Reason: reason})
			plan.Targets = append(plan.Targets, resolvedTarget{Category: id, Kind: kind, Path: resolved})
			if info, statErr := os.Stat(resolved); statErr != nil && !os.IsNotExist(statErr) {
				block("target_unreadable", id, "A reset target cannot be inspected.", "Restore access before reviewing reset.")
			} else if statErr == nil {
				wantDirectory := kind == "agent_profiles" || kind == "owned_uploads"
				if (wantDirectory && !info.IsDir()) || (!wantDirectory && !info.Mode().IsRegular()) {
					block("target_type_mismatch", id, "An owner path has an unexpected filesystem type.", "Resolve the conflicting file/directory before reset; no recursive fallback is allowed.")
				}
			}
			if root == "" || resolved == root || !containsPath(root, resolved) || containsPath(filepath.Join(root, resetstate.Directory), resolved) {
				block("target_outside_installation", id, "An active owner targets a location outside the reset installation or inside reset recovery metadata.", "Converge the owner's configuration with the owned installation; no default path will be substituted.")
			}
			for _, kept := range protected {
				if pathsOverlap(resolved, kept) {
					block("retained_path_overlap", id, "A reset target overlaps retained workspace or vault contents.", "Separate the managed state from retained files before reset; no recursive fallback is allowed.")
				}
			}
		}
		inspectCategory(ctx, owners, id, &category, report, target, block)
		view.Categories = append(view.Categories, category)
	}
	if slices.Contains(selected, CategoryAppRecords) && os.Getenv("WORKSPACE_DIR") != "" {
		block("operator_workspace_root", CategoryAppRecords, "WORKSPACE_DIR would automatically re-adopt detached workspaces.", "Remove the operator-enforced root from the launch configuration and relaunch before reset. Ori will not edit the environment.")
	}
	// Counts and record contents are deliberately excluded. Schema, resolved
	// targets, protected roots, category semantics and readiness are material.
	binding := struct {
		Version       int
		Intent        Intent
		Selected      []CategoryID
		Root          string
		Targets       []resolvedTarget
		Protected     []protectedDigest
		Schema        string
		SchemaVersion int
		DatabasePath  string
		RootConfirmed bool
		Blockers      []Blocker
	}{SchemaVersion, intent, selected, root, plan.Targets, protectedDigests, report.SchemaDigest, report.Version, databasePath, rootConfirmed, view.Blockers}
	data, err := json.Marshal(binding)
	if err != nil {
		return resolvedPlan{}, err
	}
	if len(data) > maxPlanBytes {
		return resolvedPlan{}, ErrPreviewLimit
	}
	plan.Evidence = resolvedEvidence{
		ProtectedPaths:         slices.Clone(protected),
		ProtectedDigests:       slices.Clone(protectedDigests),
		DatabasePath:           databasePath,
		DatabaseSchema:         report.SchemaDigest,
		DatabaseSchemaVersion:  report.Version,
		WorkspaceRootConfirmed: rootConfirmed,
	}
	digest := sha256.Sum256(data)
	view.ScopeDigest = hex.EncodeToString(digest[:])
	encoded, err := json.Marshal(plan)
	if err != nil || len(encoded) > maxPlanBytes {
		return resolvedPlan{}, ErrPreviewLimit
	}
	if err := ctx.Err(); err != nil {
		return resolvedPlan{}, err
	}
	return plan, nil
}

func inspectCategory(ctx context.Context, owners Owners, id CategoryID, category *CategoryPreview, report database.ResetInspection,
	target func(string, string, string), block func(string, CategoryID, string, string)) {
	unknown := func(name string) {
		category.Facts = append(category.Facts, CountFact{Name: name, UnavailableReason: "Authoritative owner or non-interactive inspection unavailable."})
	}
	switch id {
	case CategorySettings:
		if owners.Config == nil {
			block("settings_owner_unavailable", id, "The active settings owner is unavailable.", "Restore the configuration owner before reset.")
			unknown("Ori-saved provider/search key slots")
		} else {
			target("settings_fields", owners.Config.PersistencePath(), "Reset preferences and legacy provider/search fields; preserve root metadata required by unselected registrations.")
			credentials, err := owners.Config.InspectResetCredentials()
			if err != nil {
				unknown("Ori-saved provider/search key slots")
				code := "credential_inspection_failed"
				recovery := "Unlock or restore the installation-scoped secret store, then review again. vault_dek and unrelated namespaces will remain preserved."
				switch {
				case errors.Is(err, config.ErrResetCredentialStoreLocked):
					code = "credential_store_locked"
				case errors.Is(err, config.ErrResetCredentialStoreUnavailable):
					code = "credential_store_unavailable"
				case errors.Is(err, config.ErrResetCredentialStoreReadOnly):
					code = "credential_store_read_only"
				case errors.Is(err, config.ErrResetCredentialNamespace):
					code = "credential_namespace_ambiguous"
					recovery = "Move the four provider/search entries into this installation's canonical absolute-path namespace before reset. Do not delete shared legacy entries automatically. vault_dek remains preserved."
				}
				block(code, id, "Ori-saved credential slots cannot be safely inspected and deleted.", recovery)
			} else {
				present := int64(0)
				for _, slot := range credentials.Slots {
					if (slot.SecurePresent != nil && *slot.SecurePresent) || slot.LegacyPlaintext {
						present++
					}
				}
				category.Facts = append(category.Facts, CountFact{Name: "Ori-saved provider/search key slots", Count: &present})
			}
		}
		category.Retained = append(category.Retained, Location{DisplayPath: "vault_dek and unrelated secure-storage namespaces", Reason: "Encryption material and credentials outside the exact provider/search allowlist are preserved."})
		for _, name := range []string{"OPENAI_API_KEY", "ANTHROPIC_API_KEY", "GEMINI_API_KEY", "GOOGLE_API_KEY", "BRAVE_API_KEY"} {
			present := int64(0)
			if os.Getenv(name) != "" {
				present = 1
			}
			category.Facts = append(category.Facts, CountFact{Name: name + " supplied externally (preserved)", Count: &present})
		}
	case CategoryAgents:
		paths, ok := owners.Agents.(interface {
			PersistencePaths() (string, string, string)
		})
		if !ok {
			unknown("active agent profiles")
			block("agent_owner_unavailable", id, "Agent persistence locations are unavailable.", "Use the authoritative file-backed agent owner; no default agents directory will be guessed.")
		} else {
			index, profiles, projection := paths.PersistencePaths()
			target("agent_index", index, "Remove the owned agent index, not unrelated files beside it.")
			target("agent_profiles", profiles, "Remove only owned agent profiles/skills/state; preserve unknown files and external links.")
			target("agent_projection", projection, "Remove the owner's compatibility projection.")
			count := int64(len(owners.Agents.ListAgents()))
			category.Facts = append(category.Facts, CountFact{Name: "active agent profiles", Count: &count})
		}
	case CategoryAppRecords:
		if owners.Config == nil {
			block("registration_settings_unavailable", id, "Workspace registration settings are unavailable.", "Restore the settings owner so root consent can be cleared without resetting unrelated preferences or credentials.")
		} else {
			target("workspace_registration_fields", owners.Config.PersistencePath(), "Collateral effect: clear workspace root/adoption consent, not unrelated preferences or provider keys.")
		}
		if owners.Allowlist == nil {
			block("workspace_permissions_unavailable", id, "The workspace import-permission owner is unavailable.", "Restore the allowlist owner before detaching registrations.")
		} else {
			target("workspace_permissions", owners.Allowlist.PersistencePath(), "Collateral effect: clear import permissions so retained workspace snapshots stay detached.")
			count := int64(len(owners.Allowlist.IDs()))
			category.Facts = append(category.Facts, CountFact{Name: "active workspace import permissions", Count: &count})
		}
		if owners.Database != nil {
			target("database_records", owners.Database.Path(), "Clear the reviewed shared app-record domains and detach registrations only at the pre-start boundary; SQLite/WAL/SHM stay untouched while live.")
		}
		for _, name := range database.ResetRecordTables() {
			fact := CountFact{Name: name, Count: report.Counts[name]}
			if fact.Count == nil {
				fact.UnavailableReason = "Database domain unavailable; not a zero count."
			}
			category.Facts = append(category.Facts, fact)
		}
		if owners.Uploads != nil {
			target("owned_uploads", owners.Uploads.BasePath(), "Remove enumerated Ori uploads/manifests only; keep linked and unclassified files.")
			category.Facts = append(category.Facts, countFiles(ctx, owners.Uploads.BasePath()))
		} else {
			unknown("owned uploads")
			block("uploads_owner_unavailable", id, "The uploads owner is unavailable.", "Restore the uploads owner before reset; no directory default will be guessed.")
		}
	case CategorySetupSteps:
		if owners.Setup == nil {
			unknown("completed setup steps")
			block("setup_owner_unavailable", id, "The onboarding owner is unavailable.", "Restore the onboarding manager; do not delete app_state.json.")
		} else {
			target("setup_fields", owners.Setup.PersistencePath(), "Reset setup fields only; retain all identity, profile and progression fields in this file.")
			count := int64(len(owners.Setup.GetState().StepsCompleted))
			category.Facts = append(category.Facts, CountFact{Name: "completed setup steps", Count: &count})
		}
	}
}
