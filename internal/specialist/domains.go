package specialist

// Domain mapping data. This file is deliberately the only place in the host
// that names a specific application, and it holds nothing but data: copy,
// ordering hints, and the ID of a blueprint published elsewhere.
//
// It registers no route, module, runtime, template, or capability
// implementation, exactly like the inert marketplace metadata the domain
// extraction audit already excepts (see
// internal/server/reaper_extraction_audit_test.go). Everything that acts on
// these values — matching, offering, ordering, persistence — lives in
// specialist.go and the packages that consume it, and none of it knows what a
// DAW is.
//
// Adding a domain is a new entry here plus its copy. Nothing else changes.
var registryEntries = []Entry{
	{
		Slug:           "music_production",
		AppPatterns:    [][]string{{"reaper"}},
		DisplayName:    "music projects",
		SpecialistName: "Reaper Producer",
		OfferCopy: OfferCopy{
			Headline:     "I found REAPER on this Mac.",
			Question:     "Want me to help with your music projects?",
			AcceptLabel:  "Yes, help with my music",
			DeclineLabel: "No thanks",
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
			{Type: ItemPriority, Label: "Song or project in progress", Placeholder: "Which track are you on?", AddLabel: "Add a song or project"},
			{Type: ItemIOwe, Label: "Something I owe a collaborator", Placeholder: "What did you promise?", AddLabel: "Add something I owe"},
			{Type: ItemWaitingOn, Label: "Waiting on (mix, master, feature)", Placeholder: "What are you waiting for?", AddLabel: "Add something I’m waiting on"},
			{Type: ItemFixedCommitment, Label: "Release or session date", Placeholder: "Release, session, or deadline to keep visible", AddLabel: "Add a release or session date"},
		},
		AssignmentSteps: []AssignmentStep{
			{Index: 0, Title: "Songs in progress", Legend: "What are you working on right now?"},
			{Index: 1, Title: "Owed and waiting", Legend: "What do you owe a collaborator—or what are you waiting on?"},
			{Index: 2, Title: "Release and session dates", Legend: "Dates to keep visible"},
		},
		SuggestedTemplateID: "reaper-song",
		Suggestion: Suggestion{
			Title:       "Set up your music projects",
			Body:        "Install the Ori REAPER plugin, create your music production group, prepare REAPER, then create a workspace for a new or existing project. There is no project monitoring or studio team until workspace setup is confirmed. Live access is approved and verified separately for each workspace.",
			ActionLabel: "Continue reviewed setup",
			ActionRoute: "/personal-assistant?setup=specialist",
		},
		CapabilityOrder: []string{"projects", "folders", "calendar", "email"},
		SetupJourney:    legacySetup("reaper-setup.json"),
	},
}
