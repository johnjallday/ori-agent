package folderdigest

// The host owns recognition and capability metadata. Plugins and model output
// cannot register rows. Order preserves marker precedence within a folder.
// Tools without a project marker live in the final, shape-less row.
type CapabilityRow struct {
	Shape     Shape
	Markers   []Marker
	Tools     []Tool
	Blueprint ShapeBlueprint
	Offer     *CapabilityOffer
}

// CapabilityOffer is optional. Its fields include the legacy specialist
// presentation metadata so that the same row feeds both old and new callers.
type CapabilityOffer struct {
	Slug                string
	AppPatterns         [][]string
	DisplayName         string
	SpecialistName      string
	OfferCopy           OfferCopy
	FocusAreas          []FocusOption
	AssignmentLabels    []AssignmentLabel
	AssignmentSteps     []AssignmentStep
	SuggestedTemplateID string
	Suggestion          Suggestion
	CapabilityOrder     []string
	IntegrationKey      string
	IntegrationName     string
	// ProjectExtensions gates per-project offers within a shared shape.
	// Other audio tools still count toward the shape's Home portfolio.
	ProjectExtensions []string
	HomeProviderKey   string
	HomeProviderName  string
}

type OfferCopy struct {
	Headline     string `json:"headline"`
	Question     string `json:"question"`
	AcceptLabel  string `json:"accept_label"`
	DeclineLabel string `json:"decline_label"`
	AcceptedNote string `json:"accepted_note"`
	ManualLabel  string `json:"manual_label"`
}

type FocusOption struct {
	Value    string `json:"value"`
	Label    string `json:"label"`
	Selected bool   `json:"selected"`
}

type AssignmentLabel struct {
	Type        string `json:"type"`
	Label       string `json:"label"`
	Placeholder string `json:"placeholder"`
	AddLabel    string `json:"add_label"`
}

type AssignmentStep struct {
	Index  int    `json:"index"`
	Title  string `json:"title"`
	Legend string `json:"legend"`
}

type Suggestion struct {
	Title       string `json:"title"`
	Body        string `json:"body"`
	ActionLabel string `json:"action_label"`
	ActionRoute string `json:"action_route"`
}

var capabilityRows = []CapabilityRow{
	{
		Shape: ShapeAudio,
		Markers: []Marker{
			{Name: "*.rpp", Kind: MarkerGlob, Shape: ShapeAudio, Label: "REAPER session"},
			{Name: "*.logicx", Kind: MarkerGlob, Shape: ShapeAudio, Label: "Logic Pro project"},
			{Name: "*.als", Kind: MarkerGlob, Shape: ShapeAudio, Label: "Ableton Live set"},
		},
		Tools: []Tool{
			{Match: ToolByExtension, Value: ".rpp", ToolID: "reaper", ToolName: "REAPER", HypothesisText: "REAPER may be one of the tools you use."},
			{Match: ToolByExtension, Value: ".logicx", ToolID: "logic-pro", ToolName: "Logic Pro", HypothesisText: "Logic Pro may be one of the tools you use."},
			{Match: ToolByExtension, Value: ".als", ToolID: "ableton-live", ToolName: "Ableton Live", HypothesisText: "Ableton Live may be one of the tools you use."},
		},
		Blueprint: ShapeBlueprint{Shape: ShapeAudio, BlueprintID: "reaper-song", Label: "REAPER song"},
		Offer: &CapabilityOffer{
			Slug: "music_production", AppPatterns: [][]string{{"reaper"}},
			DisplayName: "music projects", SpecialistName: "Reaper Producer",
			OfferCopy: OfferCopy{
				Headline:    "I found REAPER on this Mac.",
				Question:    "Want me to help with your music projects?",
				AcceptLabel: "Yes, help with my music", DeclineLabel: "No thanks",
				AcceptedNote: "Let's connect a REAPER project so I can include real studio updates.",
				ManualLabel:  "I work on music",
			},
			FocusAreas: []FocusOption{
				{Value: "plan_my_day", Label: "Plan my studio day", Selected: true},
				{Value: "track_songs_in_progress", Label: "Track songs in progress", Selected: true},
				{Value: "chase_collaborator_handoffs", Label: "Chase collaborator handoffs", Selected: true},
				{Value: "keep_release_dates_visible", Label: "Keep release and session dates visible"},
				{Value: "organize_project_files", Label: "Keep project files organized"},
				{Value: "something_else", Label: "Something else"},
			},
			AssignmentLabels: []AssignmentLabel{
				{Type: "priority", Label: "Song or project in progress", Placeholder: "Which track are you on?", AddLabel: "Add a song or project"},
				{Type: "i_owe", Label: "Something I owe a collaborator", Placeholder: "What did you promise?", AddLabel: "Add something I owe"},
				{Type: "waiting_on", Label: "Waiting on (mix, master, feature)", Placeholder: "What are you waiting for?", AddLabel: "Add something I’m waiting on"},
				{Type: "fixed_commitment", Label: "Release or session date", Placeholder: "Release, session, or deadline to keep visible", AddLabel: "Add a release or session date"},
			},
			AssignmentSteps: []AssignmentStep{
				{Index: 0, Title: "Songs in progress", Legend: "What are you working on right now?"},
				{Index: 1, Title: "Owed and waiting", Legend: "What do you owe a collaborator—or what are you waiting on?"},
				{Index: 2, Title: "Release and session dates", Legend: "Dates to keep visible"},
			},
			SuggestedTemplateID: "reaper-song", IntegrationKey: "ori_reaper", IntegrationName: "REAPER", ProjectExtensions: []string{".rpp"}, HomeProviderKey: "music_project_management", HomeProviderName: "Music Project Management",
			Suggestion: Suggestion{
				Title:       "Set up your music projects",
				Body:        "Install the Ori REAPER plugin, create your music production group, then create a workspace for a new or existing project. There is no project monitoring or studio team until workspace setup is confirmed. Live access is approved and verified separately for each workspace.",
				ActionLabel: "Continue reviewed setup", ActionRoute: "/personal-assistant?setup=specialist",
			},
			CapabilityOrder: []string{"projects", "folders", "calendar", "email"},
		},
	},
	{
		Shape: ShapeManuscript,
		Markers: []Marker{
			{Name: "main.tex", Kind: MarkerFile, Shape: ShapeManuscript, Label: "LaTeX manuscript"},
			{Name: "chapters", Kind: MarkerDir, Shape: ShapeManuscript, Label: "chapters folder"},
			{Name: "outline.md", Kind: MarkerFile, Shape: ShapeManuscript, Label: "outline"},
		},
		Tools:     []Tool{{Match: ToolByExtension, Value: ".tex", ToolID: "latex", ToolName: "LaTeX", HypothesisText: "LaTeX may be one of the tools you use."}},
		Blueprint: ShapeBlueprint{Shape: ShapeManuscript, BlueprintID: "writing-project", Label: "Writing project"},
	},
	{
		Shape:     ShapeCorpus,
		Markers:   []Marker{{Name: "*.bib", Kind: MarkerGlob, Shape: ShapeCorpus, Label: "bibliography"}},
		Tools:     []Tool{{Match: ToolByExtension, Value: ".bib", ToolID: "reference-manager", ToolName: "a reference manager", HypothesisText: "A reference manager may be one of the tools you use."}},
		Blueprint: ShapeBlueprint{Shape: ShapeCorpus, BlueprintID: "research-project", Label: "Research project"},
	},
	{
		Shape:   ShapeNotes,
		Markers: []Marker{{Name: ".obsidian", Kind: MarkerDir, Shape: ShapeNotes, Label: "Obsidian vault"}},
		Tools:   []Tool{{Match: ToolByMarker, Value: ".obsidian", AppNames: []string{"Obsidian"}, ToolID: "obsidian", ToolName: "Obsidian", HypothesisText: "Obsidian may be one of the tools you use to keep notes."}},
	},
	{
		Shape: ShapeCode,
		Markers: []Marker{
			{Name: "go.mod", Kind: MarkerFile, Shape: ShapeCode, Label: "Go module"},
			{Name: "package.json", Kind: MarkerFile, Shape: ShapeCode, Label: "Node.js package"},
			{Name: "Cargo.toml", Kind: MarkerFile, Shape: ShapeCode, Label: "Rust crate"},
			{Name: "pyproject.toml", Kind: MarkerFile, Shape: ShapeCode, Label: "Python project"},
			{Name: "requirements.txt", Kind: MarkerFile, Shape: ShapeCode, Label: "Python project"},
			{Name: "*.xcodeproj", Kind: MarkerGlob, Shape: ShapeCode, Label: "Xcode project"},
			{Name: ".git", Kind: MarkerDir, Shape: ShapeCode, Label: "git repository"},
		},
		Tools: []Tool{
			{Match: ToolByExtension, Value: ".ipynb", ToolID: "jupyter", ToolName: "Jupyter", HypothesisText: "Jupyter may be one of the tools you use."},
			{Match: ToolByMarker, Value: "go.mod", ToolID: "go", ToolName: "Go", HypothesisText: "Go may be one of the tools you use."},
			{Match: ToolByMarker, Value: "package.json", ToolID: "nodejs", ToolName: "Node.js", HypothesisText: "Node.js may be one of the tools you use."},
			{Match: ToolByApp, AppNames: []string{"Visual Studio Code"}, ToolID: "vscode", ToolName: "Visual Studio Code", HypothesisText: "Visual Studio Code may be one of the tools you use for development."},
		},
		Blueprint: ShapeBlueprint{Shape: ShapeCode, BlueprintID: "code-project", Label: "Code project"},
	},
	{
		Tools: []Tool{
			{Match: ToolByExtension, Value: ".fig", AppNames: []string{"Figma"}, ToolID: "figma", ToolName: "Figma", HypothesisText: "Figma may be one of the tools you use for visual planning."},
			{Match: ToolByExtension, Value: ".psd", ToolID: "photoshop", ToolName: "Photoshop", HypothesisText: "Photoshop may be one of the tools you use."},
		},
	},
}

// AllCapabilities returns detached host rows. Callers cannot mutate the
// authoritative table, even through nested slices or offer copy.
func AllCapabilities() []CapabilityRow {
	out := make([]CapabilityRow, len(capabilityRows))
	for i, row := range capabilityRows {
		out[i] = cloneCapability(row)
	}
	return out
}

func cloneCapability(row CapabilityRow) CapabilityRow {
	row.Markers = append([]Marker(nil), row.Markers...)
	row.Tools = append([]Tool(nil), row.Tools...)
	for i := range row.Tools {
		row.Tools[i].AppNames = append([]string(nil), row.Tools[i].AppNames...)
	}
	if row.Offer != nil {
		offer := *row.Offer
		offer.AppPatterns = make([][]string, len(row.Offer.AppPatterns))
		for i, pattern := range row.Offer.AppPatterns {
			offer.AppPatterns[i] = append([]string(nil), pattern...)
		}
		offer.FocusAreas = append([]FocusOption(nil), offer.FocusAreas...)
		offer.AssignmentLabels = append([]AssignmentLabel(nil), offer.AssignmentLabels...)
		offer.AssignmentSteps = append([]AssignmentStep(nil), offer.AssignmentSteps...)
		offer.CapabilityOrder = append([]string(nil), offer.CapabilityOrder...)
		offer.ProjectExtensions = append([]string(nil), offer.ProjectExtensions...)
		row.Offer = &offer
	}
	return row
}

func CapabilityForShape(shape Shape) (CapabilityRow, bool) {
	if shape == "" {
		return CapabilityRow{}, false
	}
	for _, row := range capabilityRows {
		if row.Shape == shape {
			return cloneCapability(row), true
		}
	}
	return CapabilityRow{}, false
}

// Legacy lookups derive from the same rows; preserve their ordering and
// identity for existing scan callers (including marker rank and pointers).
var Markers, Tools, ShapeBlueprints = deriveTables()

func deriveTables() ([]Marker, []Tool, []ShapeBlueprint) {
	var markers []Marker
	var tools []Tool
	var blueprints []ShapeBlueprint
	for _, row := range capabilityRows {
		markers = append(markers, row.Markers...)
		tools = append(tools, row.Tools...)
		if row.Blueprint.BlueprintID != "" {
			blueprints = append(blueprints, row.Blueprint)
		}
	}
	return markers, tools, blueprints
}

// extensionKinds turns an extension into the plain word a reason line uses
// ("14 LaTeX files"). Anything missing falls back to the upper-cased
// extension ("3 MOV files").
var extensionKinds = map[string]string{
	".tex": "LaTeX", ".bib": "BibTeX", ".pdf": "PDF", ".docx": "Word", ".doc": "Word",
	".md": "Markdown", ".txt": "text", ".rtf": "rich text", ".epub": "EPUB",
	".xlsx": "Excel", ".pptx": "PowerPoint", ".csv": "CSV", ".json": "JSON",
	".xml": "XML", ".html": "HTML", ".yaml": "YAML", ".yml": "YAML",
	".go": "Go", ".py": "Python", ".js": "JavaScript", ".ts": "TypeScript",
	".swift": "Swift", ".rs": "Rust", ".java": "Java", ".rb": "Ruby", ".c": "C",
	".h": "C header", ".css": "CSS", ".sh": "shell", ".ipynb": "notebook",
	".rpp": "REAPER", ".wav": "WAV", ".aif": "AIFF", ".aiff": "AIFF", ".mp3": "MP3",
	".flac": "FLAC", ".mid": "MIDI", ".jpg": "JPEG", ".jpeg": "JPEG", ".png": "PNG",
	".heic": "HEIC", ".gif": "GIF", ".svg": "SVG", ".psd": "Photoshop", ".fig": "Figma",
	".mov": "video", ".mp4": "video", ".zip": "ZIP", ".dmg": "disk image", ".pkg": "installer",
}
