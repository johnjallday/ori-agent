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
	// Release is what the host knows about the reviewed release an install
	// would put in place, for the card's install block. Nil when it is not a
	// reviewed plugin or the host could not tell.
	Release *ReleaseFacts `json:"release,omitempty"`
}

// ReleaseFacts name a reviewed plugin release: its version when the host
// knows it, its publisher, and where its source lives. Plain text and one
// https link, never a local path.
type ReleaseFacts struct {
	Version     string `json:"version,omitempty"`
	Publisher   string `json:"publisher,omitempty"`
	SourceLabel string `json:"source_label,omitempty"`
	SourceURL   string `json:"source_url,omitempty"`
}

// cleanRelease bounds what a host reported; a source link that is not a
// plain https URL is dropped.
func cleanRelease(in ReleaseFacts) *ReleaseFacts {
	out := ReleaseFacts{
		Version:     homeFactText(in.Version),
		Publisher:   homeFactText(in.Publisher),
		SourceLabel: strings.TrimSpace(in.SourceLabel),
		SourceURL:   strings.TrimSpace(in.SourceURL),
	}
	if len(out.SourceLabel) > maxHomeFactText || strings.ContainsAny(out.SourceLabel, "\x00\r\n\\") {
		out.SourceLabel = ""
	}
	if len(out.SourceURL) > 2*maxHomeFactText || !strings.HasPrefix(out.SourceURL, "https://") ||
		strings.ContainsAny(out.SourceURL, " \t\x00\r\n\\\"'<>") {
		out.SourceURL = ""
	}
	return &out
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
	// them (the release cannot be read, or its roles do not pass the bounds
	// below); the plan then says "Fills the Home's roles".
	MissingRoles []string `json:"missing_roles,omitempty"`
	RolesKnown   bool     `json:"roles_known,omitempty"`
	// SongDetails says what the existing Home's song-details consent lets its
	// scans read from the project files of every folder placed in it, so the plan
	// does not say "nothing is read" when it is not true: SongDetailsNumbers
	// (tempo, length and track count) or SongDetailsNames (and the track names,
	// markers and regions). Empty when the Home has no active consent, or does not
	// exist yet.
	SongDetails string `json:"song_details,omitempty"`
	// Hires describes each of MissingRoles, index for index: the name it
	// would be hired under, what it does and a name it would otherwise have
	// had. The host fills it when the look runs, from the provider's reviewed
	// release, so it is known before the Home exists. An older record has
	// MissingRoles and no Hires.
	Hires []HomeHire `json:"hires,omitempty"`
}

// HomeHire is one agent the Home step would hire, in plain words.
type HomeHire struct {
	// Name is the name the agent would be hired under.
	Name string `json:"name"`
	// Job is one line on what the role does, cut from the provider's own
	// description by JobLine; empty when the provider gave none.
	Job string `json:"job,omitempty"`
	// Taken is the name the role would have had when one of the owner's
	// agents already holds it: the run never adopts that agent, it hires a
	// new one under Name. Empty when Name is the role's own name.
	Taken string `json:"taken,omitempty"`
}

// What an existing Home's consent reaches (HomeFacts.SongDetails).
const (
	SongDetailsNumbers = "numbers"
	SongDetailsNames   = "names"
)

// Bounds of HomeFacts: a Home declares a handful of roles.
const (
	maxHomeRoles    = 8
	maxHomeFactText = 100
	// MaxJobLineRunes bounds a hire's job line, in characters.
	MaxJobLineRunes = 100
)

// JobLine is a role's one-line job from the provider's own description: the
// first sentence, cut at a word to at most MaxJobLineRunes characters with an
// ellipsis. It is empty when the line would read like a path, so one odd
// description leaves one job blank and never the whole roster. Runes are
// counted, not bytes, so a name in another script is not cut short.
func JobLine(description string) string {
	text := strings.Join(strings.Fields(description), " ")
	for i := 0; i < len(text); i++ {
		if (text[i] == '.' || text[i] == '!' || text[i] == '?') && (i+1 == len(text) || text[i+1] == ' ') {
			text = text[:i+1]
			break
		}
	}
	if runes := []rune(text); len(runes) > MaxJobLineRunes {
		head := runes[:MaxJobLineRunes-1]
		// Step back to the last whole word, unless the cut already fell on one.
		if runes[MaxJobLineRunes-1] != ' ' {
			if at := strings.LastIndex(string(head), " "); at > 0 {
				head = []rune(string(head)[:at])
			}
		}
		text = strings.TrimRight(string(head), " ,;:") + "…"
	}
	if strings.ContainsAny(text, "\x00/\\") || strings.HasPrefix(text, "~") {
		return ""
	}
	return text
}

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
// leaves the roles unknown rather than silently short. A job line is bounded
// on its own (JobLine) and never takes the roster down with it.
func cleanHomeFacts(in HomeFacts) *HomeFacts {
	// A record from before the hires were described carries names only.
	hires := in.Hires
	if hires == nil {
		for _, role := range in.MissingRoles {
			hires = append(hires, HomeHire{Name: role})
		}
	}
	out := HomeFacts{
		ProviderName:    homeFactText(in.ProviderName),
		ProviderVersion: homeFactText(in.ProviderVersion),
		ProviderReady:   in.ProviderReady,
		HomeExists:      in.HomeExists,
		RolesKnown:      in.RolesKnown && len(hires) <= maxHomeRoles,
	}
	if in.HomeExists && (in.SongDetails == SongDetailsNumbers || in.SongDetails == SongDetailsNames) {
		out.SongDetails = in.SongDetails
	}
	if out.RolesKnown {
		for _, hire := range hires {
			name := homeFactText(hire.Name)
			if name == "" {
				out.RolesKnown, out.MissingRoles, out.Hires = false, nil, nil
				break
			}
			out.MissingRoles = append(out.MissingRoles, name)
			out.Hires = append(out.Hires, HomeHire{Name: name, Job: JobLine(hire.Job), Taken: homeFactText(hire.Taken)})
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
	// Release names the reviewed release of an integration (KindIntegration)
	// or a Home provider (KindHomeProvider) by its key; false when the host
	// does not know it. Nil asks nothing.
	Release func(kind, key string) (ReleaseFacts, bool)
}

// releaseOf asks the host for a reviewed release, bounded; nil when unknown.
func releaseOf(ready Readiness, kind, key string) *ReleaseFacts {
	if ready.Release == nil || key == "" {
		return nil
	}
	facts, ok := ready.Release(kind, key)
	if !ok {
		return nil
	}
	return cleanRelease(facts)
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
		c.Release = releaseOf(ready, KindHomeProvider, p.HomeProviderKey)
		if c.Release != nil && c.Release.Version == "" && c.Home != nil {
			c.Release.Version = c.Home.ProviderVersion
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
	if c.Action != ActionAlreadyHere {
		c.Release = releaseOf(ready, KindIntegration, row.Offer.IntegrationKey)
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

// IntegrationInstalls reports whether running an integration candidate
// downloads and installs its plugin: every action but already here, whether
// or not the application is on this Mac.
func IntegrationInstalls(c Candidate) bool {
	return c.Kind == KindIntegration && c.Action != ActionAlreadyHere
}

// AppToolsSentence says where the application and its tools stand, for an
// integration that installs: the application on this Mac without its tools,
// or neither yet. Empty when the candidate installs nothing or names no app.
func AppToolsSentence(c Candidate) string {
	if !IntegrationInstalls(c) || c.App == "" {
		return ""
	}
	if c.Action == ActionConnect {
		return c.App + " is on this Mac; its Ori tools are not yet."
	}
	return c.App + " is not on this Mac yet; the tools install anyway and work once it is."
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
