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
		if ready.HomeExists != nil && ready.HomeExists(p.HomeProviderKey) {
			c.Action, c.Detail = ActionAlreadyHere, "adds this folder to your "+home
		}
		out = append(out, c)
		// The portfolio's own project format still needs its tools.
		if integration, ok := integrationFor(dominantProject(facts), ready); ok {
			out = append(out, integration)
		}
	}
	if stage == foldermap.StageTools && facts.RootProject != nil {
		if integration, ok := integrationFor(facts.RootProject, ready); ok {
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
