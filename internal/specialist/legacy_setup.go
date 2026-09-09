package specialist

import "embed"

// Frozen bootstraps for published pre-quest integrations. Installed declarations
// take precedence in setupjourney; these are explicitly host compatibility data.
//
//go:embed compatibility/*.json
var legacySetupDeclarations embed.FS

func legacySetup(name string) *SetupJourney {
	data, err := legacySetupDeclarations.ReadFile("compatibility/" + name)
	if err != nil {
		panic("missing compiled compatibility setup declaration")
	}
	declaration, err := ParseSetupJourney(data)
	if err != nil {
		panic("invalid compiled compatibility setup declaration")
	}
	return declaration
}
