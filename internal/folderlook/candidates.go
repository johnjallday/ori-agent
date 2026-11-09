package folderlook

import (
	"strings"

	"github.com/johnjallday/ori-agent/internal/folderdigest"
	"github.com/johnjallday/ori-agent/internal/foldermap"
)

// Candidate kinds.
const (
	KindIntegration  = "integration"
	KindHomeProvider = "home_provider"
	KindFileCurator  = "file_curator"
	KindSkill        = "skill"
	KindMCP          = "mcp"
	KindPlugin       = "plugin"
)

// Candidate sources (PRD FR19).
const (
	SourceTable        = "table"
	SourceInstalledApp = "installed_app"
	SourceInstalled    = "installed"
	SourceSearch       = "search"
)

// Candidate actions.
const (
	ActionInstall     = "install"
	ActionConnect     = "connect"
	ActionAlreadyHere = "already_here"
	ActionAdd         = "add"
)

// Candidate is one tool the host proposes. It is inert: nothing installs,
// connects or binds until Set it up, and only when the user left it selected.
type Candidate struct {
	// ID is stable across looks: "integration:<key>", "home:<key>",
	// "file-curator", or a plugin, skill or server id from search.
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	Name     string `json:"name"`
	Source   string `json:"source"`
	Action   string `json:"action"`
	Detail   string `json:"detail,omitempty"`
	Why      string `json:"why,omitempty"`
	Required bool   `json:"required,omitempty"`
	// Ranked is set when the agent ranked this candidate; Why is then the
	// agent's line, provenance on the report.
	Ranked bool `json:"ranked,omitempty"`
	// Marketplace labels a search hit ("Found by search · <marketplace>").
	Marketplace string `json:"marketplace,omitempty"`
	// IntegrationKey and HomeProviderKey are the reviewed identities a Set it
	// up run acts on. Never shown.
	IntegrationKey  string `json:"integration_key,omitempty"`
	HomeProviderKey string `json:"home_provider_key,omitempty"`
	App             string `json:"app,omitempty"`
	// Ref is a search hit's own marketplace id, case kept, for the install.
	Ref string `json:"ref,omitempty"`
	// Home is what the host knew about a Home provider's candidate when it
	// looked: the plan words one line per action from it.
	Home *HomeFacts `json:"home,omitempty"`
}

// HomeFacts are what the host knows about a Home provider and the owner's
// Home of it: plain names and a version, never a path or an id.
type HomeFacts struct {
	// ProviderName is the reviewed provider's display name; ProviderVersion
	// the reviewed release a Set it up would put in place.
	ProviderName    string `json:"provider_name,omitempty"`
	ProviderVersion string `json:"provider_version,omitempty"`
	// ProviderReady is true when that release is installed and enabled.
	ProviderReady bool `json:"provider_ready,omitempty"`
	// HomeExists is true when the owner already has this provider's Home.
	HomeExists bool `json:"home_exists,omitempty"`
	// MissingRoles are the agents the Home still needs, by the names they
	// would be hired under. RolesKnown is false when the host cannot know
	// them before the Home exists; the plan then says "Fills the Home's roles".
	MissingRoles []string `json:"missing_roles,omitempty"`
	RolesKnown   bool     `json:"roles_known,omitempty"`
}

// Bounds of HomeFacts: a Home declares a handful of roles.
const (
	maxHomeRoles    = 8
	maxHomeFactText = 100
)

// homeFactText keeps one name or version: trimmed, one line, short, and not
// shaped like a path.
func homeFactText(text string) string {
	text = strings.TrimSpace(text)
	if len(text) > maxHomeFactText || strings.ContainsAny(text, "\x00\r\n/\\") || strings.HasPrefix(text, "~") {
		return ""
	}
	return text
}

// cleanHomeFacts bounds what a host reported. A role name that does not pass
// leaves the roles unknown rather than silently short.
func cleanHomeFacts(in HomeFacts) *HomeFacts {
	out := HomeFacts{
		ProviderName:    homeFactText(in.ProviderName),
		ProviderVersion: homeFactText(in.ProviderVersion),
		ProviderReady:   in.ProviderReady,
		HomeExists:      in.HomeExists,
		RolesKnown:      in.RolesKnown && len(in.MissingRoles) <= maxHomeRoles,
	}
	if out.RolesKnown {
		for _, role := range in.MissingRoles {
			name := homeFactText(role)
			if name == "" {
				out.RolesKnown, out.MissingRoles = false, nil
				break
			}
			out.MissingRoles = append(out.MissingRoles, name)
		}
	}
	return &out
}

// Readiness tells the candidate builder what the user already has.
type Readiness struct {
	// InstalledApps are the applications DetectInstalledApps found.
	InstalledApps []folderdigest.InstalledApp
	// IntegrationReady says a reviewed integration is installed, enabled and
	// at the reviewed release.
	IntegrationReady func(key string) bool
	// HomeExists says the user already has the Home a provider supplies.
	HomeExists func(providerKey string) bool
	// Home reports a provider's install state and the owner's Home of it;
	// false when the host could not tell (the plan then keeps its generic
	// lines). Nil asks nothing.
	Home func(providerKey string) (HomeFacts, bool)
}

// RequiredCandidates are the candidates the stage needs (FR20): the
// integration for a project at the fed folder, the Home provider for a
// portfolio, File Curator for a dump. They come from the host's capability
// table and installed-app check only, so they exist with or without a model.
func RequiredCandidates(facts foldermap.Facts, ready Readiness) []Candidate {
	var out []Candidate
	stage := foldermap.StageFor(facts)
	if stage == foldermap.StageHome && facts.Portfolio != nil {
		p := facts.Portfolio
		home := p.HomeName
		if home == "" {
			home = "its Home"
		}
		c := Candidate{
			ID: "home:" + p.HomeProviderKey, Kind: KindHomeProvider, Name: home, Source: SourceTable,
			Action: ActionInstall, Required: true, HomeProviderKey: p.HomeProviderKey,
			Detail: "creates your " + home,
			Why:    "Keeps every " + foldermap.ShapeNoun(p.Shape, 1) + " here on one shelf, at any depth.",
		}
		exists := false
		if ready.Home != nil {
			if facts, ok := ready.Home(p.HomeProviderKey); ok {
				c.Home = cleanHomeFacts(facts)
				exists = c.Home.HomeExists
			}
		}
		if c.Home == nil && ready.HomeExists != nil {
			exists = ready.HomeExists(p.HomeProviderKey)
		}
		if exists {
			c.Action, c.Detail = ActionAlreadyHere, "adds this folder to your "+home
		}
		out = append(out, c)
		// The portfolio's own project format still needs its tools.
		if integration, ok := integrationFor(dominantProject(facts), ready); ok {
			out = append(out, integration)
		}
	}
	if stage == foldermap.StageTools {
		if integration, ok := integrationFor(facts.SoleProject(), ready); ok {
			out = append(out, integration)
		}
	}
	if facts.Dump && facts.RootProject == nil {
		out = append(out, Candidate{
			ID: "file-curator", Kind: KindFileCurator, Name: "File Curator", Source: SourceTable,
			Action: ActionAdd, Required: true, Detail: "keeps the loose files here tidy",
			Why: "The files sitting directly in this folder are of many kinds.",
		})
	}
	return out
}

// integrationFor is the reviewed integration for one project's format, worded
// connect (the application is on this Mac) or install.
func integrationFor(p *foldermap.Project, ready Readiness) (Candidate, bool) {
	if p == nil {
		return Candidate{}, false
	}
	row, ok := folderdigest.ProjectCapabilityFor(p.Shape, p.Marker, "")
	if !ok || row.Offer == nil || row.Offer.IntegrationKey == "" {
		return Candidate{}, false
	}
	name := row.Offer.IntegrationName
	if name == "" {
		name = foldermap.FormatName(p.Marker)
	}
	c := Candidate{
		ID: "integration:" + row.Offer.IntegrationKey, Kind: KindIntegration, Name: name + " tools",
		Source: SourceTable, Action: ActionInstall, Required: true, IntegrationKey: row.Offer.IntegrationKey,
		App: foldermap.FormatName(p.Marker), Detail: "install, " + name + " is not on this Mac",
		Why: "Lets the agent work with the " + name + " " + foldermap.ShapeNoun(p.Shape, 2) + " here.",
	}
	if appInstalled(c.App, ready.InstalledApps) {
		c.Source, c.Action, c.Detail = SourceInstalledApp, ActionConnect, "connect, "+name+" is installed"
	}
	if ready.IntegrationReady != nil && ready.IntegrationReady(row.Offer.IntegrationKey) {
		c.Source, c.Action, c.Detail = SourceInstalled, ActionAlreadyHere, "already here"
	}
	return c, true
}

// dominantProject is the first project of the most common shape and format.
func dominantProject(facts foldermap.Facts) *foldermap.Project {
	if len(facts.Shapes) == 0 {
		return nil
	}
	top := facts.Shapes[0]
	for i := range facts.Members {
		if facts.Members[i].Shape == top.Shape && facts.Members[i].Format == top.Format {
			return &facts.Members[i]
		}
	}
	return facts.RootProject
}

func appInstalled(name string, apps []folderdigest.InstalledApp) bool {
	if name == "" {
		return false
	}
	for _, app := range apps {
		if strings.EqualFold(app.Name, name) {
			return true
		}
	}
	return false
}
