package folderdigest

// The host-owned tables behind the folder scan: project markers with their
// shapes (FR14), the extension/marker → tool table shared with the saved-app
// producer (FR36), and the plain words reason lines use for file kinds.
//
// This file is data only — no imports, functions, types, or constants — so
// naming a tool such as REAPER or LaTeX here is inert metadata, the same
// exception the compiled-domain audit grants the specialist mapping.
// Everything that acts on these rows (matching, ranking, verdicts, copy) is
// generic and lives beside them. Adding a marker, a tool, or a kind name is
// a row here, not new logic.

// Markers is the marker table. Order is precedence: when a folder carries
// several markers the first matching row decides its shape, so the more
// specific shapes (an audio session, a manuscript, a corpus, a notes vault)
// come before the generic code markers a thesis or a vault may also carry
// (a .git directory, for instance).
var Markers = []Marker{
	// Audio
	{Name: "*.rpp", Kind: MarkerGlob, Shape: ShapeAudio, Label: "REAPER session"},
	{Name: "*.logicx", Kind: MarkerGlob, Shape: ShapeAudio, Label: "Logic Pro project"},
	{Name: "*.als", Kind: MarkerGlob, Shape: ShapeAudio, Label: "Ableton Live set"},
	// Manuscript
	{Name: "main.tex", Kind: MarkerFile, Shape: ShapeManuscript, Label: "LaTeX manuscript"},
	{Name: "chapters", Kind: MarkerDir, Shape: ShapeManuscript, Label: "chapters folder"},
	{Name: "outline.md", Kind: MarkerFile, Shape: ShapeManuscript, Label: "outline"},
	// Corpus
	{Name: "*.bib", Kind: MarkerGlob, Shape: ShapeCorpus, Label: "bibliography"},
	// Notes
	{Name: ".obsidian", Kind: MarkerDir, Shape: ShapeNotes, Label: "Obsidian vault"},
	// Code
	{Name: ".git", Kind: MarkerDir, Shape: ShapeCode, Label: "git repository"},
	{Name: "package.json", Kind: MarkerFile, Shape: ShapeCode, Label: "Node.js package"},
	{Name: "go.mod", Kind: MarkerFile, Shape: ShapeCode, Label: "Go module"},
	{Name: "Cargo.toml", Kind: MarkerFile, Shape: ShapeCode, Label: "Rust crate"},
	{Name: "pyproject.toml", Kind: MarkerFile, Shape: ShapeCode, Label: "Python project"},
	{Name: "requirements.txt", Kind: MarkerFile, Shape: ShapeCode, Label: "Python project"},
	{Name: "*.xcodeproj", Kind: MarkerGlob, Shape: ShapeCode, Label: "Xcode project"},
}

// Tools is the tool table. Values are lower-case; extensions carry the dot.
var Tools = []Tool{
	{Match: ToolByExtension, Value: ".rpp", ToolID: "reaper", ToolName: "REAPER", HypothesisText: "REAPER may be one of the tools you use."},
	{Match: ToolByExtension, Value: ".logicx", ToolID: "logic-pro", ToolName: "Logic Pro", HypothesisText: "Logic Pro may be one of the tools you use."},
	{Match: ToolByExtension, Value: ".als", ToolID: "ableton-live", ToolName: "Ableton Live", HypothesisText: "Ableton Live may be one of the tools you use."},
	{Match: ToolByExtension, Value: ".tex", ToolID: "latex", ToolName: "LaTeX", HypothesisText: "LaTeX may be one of the tools you use."},
	{Match: ToolByExtension, Value: ".ipynb", ToolID: "jupyter", ToolName: "Jupyter", HypothesisText: "Jupyter may be one of the tools you use."},
	{Match: ToolByMarker, Value: "go.mod", ToolID: "go", ToolName: "Go", HypothesisText: "Go may be one of the tools you use."},
	{Match: ToolByMarker, Value: "package.json", ToolID: "nodejs", ToolName: "Node.js", HypothesisText: "Node.js may be one of the tools you use."},
	{Match: ToolByExtension, Value: ".fig", AppNames: []string{"Figma"}, ToolID: "figma", ToolName: "Figma", HypothesisText: "Figma may be one of the tools you use for visual planning."},
	{Match: ToolByExtension, Value: ".psd", ToolID: "photoshop", ToolName: "Photoshop", HypothesisText: "Photoshop may be one of the tools you use."},
	{Match: ToolByExtension, Value: ".bib", ToolID: "reference-manager", ToolName: "a reference manager", HypothesisText: "A reference manager may be one of the tools you use."},
	{Match: ToolByMarker, Value: ".obsidian", AppNames: []string{"Obsidian"}, ToolID: "obsidian", ToolName: "Obsidian", HypothesisText: "Obsidian may be one of the tools you use to keep notes."},
	{Match: ToolByApp, AppNames: []string{"Visual Studio Code"}, ToolID: "vscode", ToolName: "Visual Studio Code", HypothesisText: "Visual Studio Code may be one of the tools you use for development."},
}

// ShapeBlueprints maps a shape to the blueprint a workspace created from the
// folder starts with (FR28). A shape with no row, or a blueprint that is not
// installed, starts from the blank workspace instead.
var ShapeBlueprints = []ShapeBlueprint{
	{Shape: ShapeAudio, BlueprintID: "reaper-song", Label: "REAPER song"},
	{Shape: ShapeManuscript, BlueprintID: "writing-project", Label: "Writing project"},
	{Shape: ShapeCorpus, BlueprintID: "research-project", Label: "Research project"},
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
