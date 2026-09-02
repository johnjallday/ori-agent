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
var registry = []Entry{
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
			AcceptedNote: "Your assistant will keep an eye on your music projects and tell you what Reaper Producer has done.",
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
			Title:       "Set up your studio workspace",
			Body:        "The Reaper Song blueprint brings in Reaper Producer, the named expert who handles studio work. Your assistant reports on what it does.",
			ActionLabel: "Create the studio workspace",
			ActionRoute: "/?create=1&blueprint=reaper-song",
		},
		CapabilityOrder: []string{"projects", "folders", "calendar", "email"},
	},
}
