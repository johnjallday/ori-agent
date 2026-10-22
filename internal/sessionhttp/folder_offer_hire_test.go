package sessionhttp

import (
	"errors"
	"testing"
)

// The hire a folder card names for an installed blueprint is the project role's
// fill the Create Workspace review would preview: the role's new agent, named
// as the create would name it. Reading it creates nothing.
func TestFolderOfferHireNamesTheProjectRolesNewAgent(t *testing.T) {
	ready := true
	handler, tpl, cleanup := autoFillEnv(t, &ready)
	defer cleanup()

	hire, err := handler.FolderOfferHire(tpl.ID, "local", "standalone")
	if err != nil {
		t.Fatal(err)
	}
	if hire.Mode != FolderOfferHireCreate || hire.AgentName != "Lead" || hire.RoleLabel != "Lead" || hire.Goal != tpl.Goal {
		t.Fatalf("hire = %+v", hire)
	}
	if _, found := handler.agentStore.GetAgent("Lead"); found {
		t.Fatal("reading the hire created the agent")
	}
	// A saved agent that takes the name moves the new one aside, as the create
	// would; the card names the agent the run will really make.
	if err := handler.agentStore.CreateAgent("Lead", nil); err != nil {
		t.Fatal(err)
	}
	if hire, err := handler.FolderOfferHire(tpl.ID, "local", "standalone"); err != nil || hire.AgentName != "Lead 2" || hire.Mode != FolderOfferHireCreate {
		t.Fatalf("hire after a name was taken = %+v, %v", hire, err)
	}
}

func TestFolderOfferHireRefusesABlueprintItCannotRead(t *testing.T) {
	ready := true
	handler, _, cleanup := autoFillEnv(t, &ready)
	defer cleanup()
	if _, err := handler.FolderOfferHire("plugin:missing:blueprint", "local", "grouped"); !errors.Is(err, ErrFolderOfferRoster) {
		t.Fatalf("err = %v", err)
	}
	if _, err := handler.FolderOfferHire("", "local", "grouped"); !errors.Is(err, ErrFolderOfferRoster) {
		t.Fatalf("empty id: err = %v", err)
	}
}
