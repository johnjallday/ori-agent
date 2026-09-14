package projecttemplates

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// Group Templates are an inert, host-derived projection over trusted blueprint
// declarations. A managed entry describes only the Home portion of one
// source-scoped Assistant Program: it is never a project template, never an
// authorable file, and never an authority for owner, plugin, or program
// identity. Every mutation re-derives this projection and resolves canonical
// Home state through workspace.AssistantProgramStore.

type GroupTemplateKind string

const (
	GroupTemplateKindOrdinary GroupTemplateKind = "ordinary_group"
	GroupTemplateKindManaged  GroupTemplateKind = "managed_home"

	GroupTemplateGeneralID = "general"
	groupTemplateIDPrefix  = "group-template:"
)

type GroupTemplateSourceKind string

const (
	GroupTemplateSourcePlugin       GroupTemplateSourceKind = "plugin"
	GroupTemplateSourceUserTemplate GroupTemplateSourceKind = "user_template"
)

// GroupTemplateSourceState is the source-only readiness of a managed entry.
// Home existence and staffing are combined with it by the HTTP owner.
type GroupTemplateSourceState string

const (
	GroupTemplateSourceReady       GroupTemplateSourceState = "ready"
	GroupTemplateSourceUnavailable GroupTemplateSourceState = "unavailable"
	GroupTemplateSourceConflict    GroupTemplateSourceState = "conflict"
)

// GroupTemplateCandidate is one catalog entry offered to the projection.
// Usable is decided by the catalog owner from the same lifecycle and readiness
// state that gates creation; the projection never re-derives it from display
// fields.
type GroupTemplateCandidate struct {
	Template          Template
	Usable            bool
	UnavailableReason string
}

type GroupTemplateProvider struct {
	Kind          GroupTemplateSourceKind `json:"kind"`
	PluginID      string                  `json:"plugin_id,omitempty"`
	PluginVersion string                  `json:"plugin_version,omitempty"`
	TemplateName  string                  `json:"template_name,omitempty"`
}

type GroupTemplateRole struct {
	RoleID      string `json:"role_id"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required"`
	Primary     bool   `json:"primary"`
}

type GroupTemplate struct {
	ID                string                   `json:"id"`
	Kind              GroupTemplateKind        `json:"kind"`
	Revision          string                   `json:"revision,omitempty"`
	Name              string                   `json:"name"`
	Description       string                   `json:"description,omitempty"`
	Provider          *GroupTemplateProvider   `json:"provider,omitempty"`
	ProposedGroupName string                   `json:"proposed_group_name,omitempty"`
	HomeRoles         []GroupTemplateRole      `json:"home_roles,omitempty"`
	ProjectRoles      []string                 `json:"project_roles_note,omitempty"`
	SourceState       GroupTemplateSourceState `json:"-"`
	SourceReason      string                   `json:"-"`
	MissingHome       MissingHomePolicy        `json:"-"`
	HomeDigest        string                   `json:"-"`
	// Representative is the deterministic trusted source used for review,
	// Home lookup, and verified role projection. It is nil for General and
	// for a conflicting entry.
	Representative *Template `json:"-"`
}

// GeneralGroupTemplate is the ordinary group option. Its creation remains the
// reviewed Group Manager roster; it carries no program or source identity.
func GeneralGroupTemplate() GroupTemplate {
	return GroupTemplate{
		ID:          GroupTemplateGeneralID,
		Kind:        GroupTemplateKindOrdinary,
		Name:        "General",
		Description: "A home for related workspaces with a reviewed Group Manager.",
		SourceState: GroupTemplateSourceReady,
	}
}

type groupTemplateSource struct {
	candidate GroupTemplateCandidate
	kind      GroupTemplateSourceKind
	groupKey  string
	rank      int
}

// ProjectGroupTemplates returns General followed by one managed entry per
// distinct source-scoped Assistant Program, ordered by display name and ID.
// It performs no I/O.
func ProjectGroupTemplates(candidates []GroupTemplateCandidate) []GroupTemplate {
	grouped := make(map[string][]groupTemplateSource)
	for _, candidate := range candidates {
		source, ok := eligibleGroupTemplateSource(candidate)
		if !ok {
			continue
		}
		grouped[source.groupKey] = append(grouped[source.groupKey], source)
	}

	managed := make([]GroupTemplate, 0, len(grouped))
	for groupKey, sources := range grouped {
		managed = append(managed, projectGroupTemplate(groupKey, sources))
	}
	sort.Slice(managed, func(i, j int) bool {
		left, right := strings.ToLower(managed[i].Name), strings.ToLower(managed[j].Name)
		if left != right {
			return left < right
		}
		return managed[i].ID < managed[j].ID
	})
	return append([]GroupTemplate{GeneralGroupTemplate()}, managed...)
}

// FindGroupTemplate resolves an opaque selection against a freshly derived
// projection. It never parses the ID back into ownership.
func FindGroupTemplate(templates []GroupTemplate, id string) (GroupTemplate, bool) {
	id = strings.TrimSpace(id)
	for _, template := range templates {
		if template.ID == id {
			return template, true
		}
	}
	return GroupTemplate{}, false
}

func eligibleGroupTemplateSource(candidate GroupTemplateCandidate) (groupTemplateSource, bool) {
	template := candidate.Template
	program := template.AssistantProgram
	requirement := template.GroupRequirement
	if program == nil || template.HasInvalidAssistantProgram() || program.SchemaVersion != workspace.AssistantProgramSchemaVersion {
		return groupTemplateSource{}, false
	}
	// Legacy absence, None, and invalid policies stay out: this projection
	// never invents a grouped policy for a source that did not declare one.
	if requirement == nil || template.HasInvalidGroupRequirement() || !requirement.IsGrouped() ||
		requirement.AssistantProgramID != normalizeAssistantProgramID(program.ID) {
		return groupTemplateSource{}, false
	}
	programID := normalizeAssistantProgramID(program.ID)
	source := groupTemplateSource{candidate: candidate}
	switch {
	case template.PluginOwner != nil:
		pluginID := strings.ToLower(strings.TrimSpace(template.PluginOwner.PluginID))
		if pluginID == "" {
			return groupTemplateSource{}, false
		}
		source.kind, source.rank = GroupTemplateSourcePlugin, 0
		source.groupKey = groupTemplateKey(source.kind, pluginID, programID)
	case template.TemplateVariant != nil:
		// A variant cannot redefine its source's program; only a ready one
		// proves that its pinned declaration is the current source.
		if template.HasInvalidVariant() || (template.VariantSourceState != "" && template.VariantSourceState != VariantSourceReady) {
			return groupTemplateSource{}, false
		}
		pluginID := strings.ToLower(strings.TrimSpace(template.TemplateVariant.Source.PluginID))
		if pluginID == "" {
			return groupTemplateSource{}, false
		}
		source.kind, source.rank = GroupTemplateSourcePlugin, 1
		source.groupKey = groupTemplateKey(source.kind, pluginID, programID)
	case template.UserSetupQuest != nil:
		templateID := strings.ToLower(strings.TrimSpace(template.ID))
		attachmentID := strings.ToLower(strings.TrimSpace(template.UserSetupQuest.AttachmentID))
		if templateID == "" || attachmentID == "" || strings.TrimSpace(template.UserSetupQuestError) != "" {
			return groupTemplateSource{}, false
		}
		source.kind, source.rank = GroupTemplateSourceUserTemplate, 2
		source.groupKey = groupTemplateKey(source.kind, templateID+"\x00"+attachmentID, programID)
	default:
		// An unowned library copy has no valid Home key and cannot
		// impersonate a plugin or attachment contribution.
		return groupTemplateSource{}, false
	}
	return source, true
}

func groupTemplateKey(kind GroupTemplateSourceKind, identity, programID string) string {
	return string(kind) + "\x00" + identity + "\x00" + programID
}

func projectGroupTemplate(groupKey string, sources []groupTemplateSource) GroupTemplate {
	sort.SliceStable(sources, func(i, j int) bool {
		if sources[i].candidate.Usable != sources[j].candidate.Usable {
			return sources[i].candidate.Usable
		}
		if sources[i].rank != sources[j].rank {
			return sources[i].rank < sources[j].rank
		}
		return sources[i].candidate.Template.ID < sources[j].candidate.Template.ID
	})
	representative := sources[0]
	program := representative.candidate.Template.AssistantProgram
	digest := GroupTemplateHomeDigest(program)
	entry := GroupTemplate{
		ID:          GroupTemplateID(groupKey),
		Kind:        GroupTemplateKindManaged,
		Name:        program.StationName,
		Description: program.StationDescription,
		Provider:    groupTemplateProvider(representative),
		HomeDigest:  digest,
	}
	for _, role := range program.Roles {
		switch role.Scope {
		case workspace.AssistantRoleScopeHome:
			entry.HomeRoles = append(entry.HomeRoles, GroupTemplateRole{
				RoleID: role.ID, Label: role.Label, Description: role.Description,
				Required: role.Required, Primary: role.Primary,
			})
		case workspace.AssistantRoleScopeProject:
			entry.ProjectRoles = append(entry.ProjectRoles, role.Label)
		}
	}
	for _, source := range sources[1:] {
		if GroupTemplateHomeDigest(source.candidate.Template.AssistantProgram) != digest {
			// Two trusted sources disagree about what the Home is. Neither
			// may create or reuse it until the declarations converge.
			entry.SourceState = GroupTemplateSourceConflict
			entry.SourceReason = "home_declaration_conflict"
			return entry
		}
	}

	template := cloneTemplate(representative.candidate.Template)
	entry.Representative = &template
	entry.MissingHome = template.GroupRequirement.MissingHome
	entry.Revision = groupTemplateRevision(digest, template)
	if !representative.candidate.Usable {
		entry.SourceState = GroupTemplateSourceUnavailable
		entry.SourceReason = strings.TrimSpace(representative.candidate.UnavailableReason)
		if entry.SourceReason == "" {
			entry.SourceReason = "source_unavailable"
		}
		return entry
	}
	entry.SourceState = GroupTemplateSourceReady
	if entry.MissingHome == MissingHomeOfferCreate {
		entry.ProposedGroupName = template.GroupRequirement.DefaultHomeName
	}
	return entry
}

// GroupTemplateID is the opaque, owner-free selection identity for one
// source-scoped program key.
func GroupTemplateID(groupKey string) string {
	digest := sha256.Sum256([]byte("group-template:v1\x00" + groupKey))
	return groupTemplateIDPrefix + hex.EncodeToString(digest[:16])
}

// GroupTemplateHomeDigest fingerprints the Home-defining declaration: the
// whole Assistant Program except project-scoped roles, which may differ
// between blueprints that share one Home.
func GroupTemplateHomeDigest(program *workspace.AssistantProgramDeclaration) string {
	if program == nil {
		return ""
	}
	home := workspace.CloneAssistantProgramDeclaration(program)
	roles := home.Roles[:0]
	for _, role := range home.Roles {
		if role.Scope != workspace.AssistantRoleScopeProject {
			roles = append(roles, role)
		}
	}
	home.Roles = roles
	return groupTemplateDigest(home)
}

func groupTemplateRevision(homeDigest string, template Template) string {
	revision := struct {
		HomeDigest             string                         `json:"home_digest"`
		TemplateID             string                         `json:"template_id"`
		TemplateRevision       string                         `json:"template_revision,omitempty"`
		VariantRevision        string                         `json:"variant_revision,omitempty"`
		VariantSource          *TemplateVariantSource         `json:"variant_source,omitempty"`
		PluginOwner            *workspace.PluginTemplateOwner `json:"plugin_owner,omitempty"`
		UserSetupQuestRevision string                         `json:"user_setup_quest_revision,omitempty"`
	}{
		HomeDigest: homeDigest, TemplateID: template.ID, TemplateRevision: template.Revision,
		VariantRevision: template.VariantRevision, PluginOwner: template.PluginOwner,
		UserSetupQuestRevision: template.UserSetupQuestRevision,
	}
	if template.TemplateVariant != nil {
		source := template.TemplateVariant.Source
		revision.VariantSource = &source
	}
	return groupTemplateDigest(revision)
}

func groupTemplateProvider(source groupTemplateSource) *GroupTemplateProvider {
	template := source.candidate.Template
	switch {
	case template.PluginOwner != nil:
		return &GroupTemplateProvider{Kind: GroupTemplateSourcePlugin, PluginID: template.PluginOwner.PluginID, PluginVersion: template.PluginOwner.PluginVersion}
	case template.TemplateVariant != nil:
		return &GroupTemplateProvider{Kind: GroupTemplateSourcePlugin, PluginID: template.TemplateVariant.Source.PluginID, PluginVersion: template.TemplateVariant.Source.PluginVersion}
	default:
		return &GroupTemplateProvider{Kind: GroupTemplateSourceUserTemplate, TemplateName: template.Name}
	}
}

func groupTemplateDigest(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}
