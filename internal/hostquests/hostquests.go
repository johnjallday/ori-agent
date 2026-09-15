// Package hostquests holds the setup quests compiled into Ori for its built-in
// templates. It is data only: each declaration is embedded JSON normalized once
// at startup through the same strict parser as every other setup journey, and
// an invalid declaration fails startup the way an invalid built-in specialist
// does. Behavior for every step kind lives in the host, never here.
package hostquests

import (
	"embed"
	"fmt"
	"io/fs"
	"sort"

	"github.com/johnjallday/ori-agent/internal/specialist"
)

const (
	// EmailOpsSetupQuestID is the host quest that sets up the Email Ops mailbox.
	EmailOpsSetupQuestID = "email_ops_setup"
	// EmailOpsSetupQuestURL opens that quest from Home. The parameters are a
	// request only; the server validates the ID against this catalog.
	EmailOpsSetupQuestURL = "/?setup=quest&source=host&quest=" + EmailOpsSetupQuestID
)

//go:embed *.json
var declarationFiles embed.FS

var declarations = mustLoad(declarationFiles)

func mustLoad(files fs.FS) []specialist.SetupJourney {
	result, err := load(files)
	if err != nil {
		panic(fmt.Sprintf("invalid host setup quest: %v", err))
	}
	return result
}

func load(files fs.FS) ([]specialist.SetupJourney, error) {
	entries, err := fs.ReadDir(files, ".")
	if err != nil {
		return nil, err
	}
	result := make([]specialist.SetupJourney, 0, len(entries))
	seen := make(map[string]string, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		data, err := fs.ReadFile(files, entry.Name())
		if err != nil {
			return nil, fmt.Errorf("%s: %w", entry.Name(), err)
		}
		declaration, err := specialist.ParseSetupJourney(data)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", entry.Name(), err)
		}
		if err := validateHostDeclaration(declaration); err != nil {
			return nil, fmt.Errorf("%s: %w", entry.Name(), err)
		}
		if previous, duplicate := seen[declaration.ID]; duplicate {
			return nil, fmt.Errorf("%s: quest id %q is already declared by %s", entry.Name(), declaration.ID, previous)
		}
		seen[declaration.ID] = entry.Name()
		result = append(result, *declaration)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result, nil
}

// validateHostDeclaration keeps host quests to the host-owned account-link
// shape. The specialist shape belongs to reviewed integrations, and no host
// declaration may claim a plugin owner.
func validateHostDeclaration(declaration *specialist.SetupJourney) error {
	if declaration.OwnerPluginID != "" {
		return fmt.Errorf("quest %q must not name a plugin owner", declaration.ID)
	}
	if declaration.Shape() != specialist.SetupJourneyShapeAccountLink {
		return fmt.Errorf("quest %q must use the %s shape", declaration.ID, specialist.SetupJourneyShapeAccountLink)
	}
	return nil
}

// All returns independent normalized copies of every host quest, sorted by ID.
func All() []specialist.SetupJourney {
	result := make([]specialist.SetupJourney, 0, len(declarations))
	for index := range declarations {
		result = append(result, clone(declarations[index]))
	}
	return result
}

// Get returns an independent copy of one host quest.
func Get(id string) (specialist.SetupJourney, bool) {
	for index := range declarations {
		if declarations[index].ID == id {
			return clone(declarations[index]), true
		}
	}
	return specialist.SetupJourney{}, false
}

func clone(source specialist.SetupJourney) specialist.SetupJourney {
	copy := source
	copy.Steps = append([]specialist.SetupJourneyStep(nil), source.Steps...)
	if source.WorkspaceLaunch != nil {
		launch := *source.WorkspaceLaunch
		copy.WorkspaceLaunch = &launch
	}
	return copy
}
