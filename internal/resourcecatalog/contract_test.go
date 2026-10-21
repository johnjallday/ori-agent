package resourcecatalog

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/workspacesurface"
)

type contractVector struct {
	Name   string          `json:"name"`
	Answer json.RawMessage `json:"answer"`
	Raw    string          `json:"raw"`
}

func readContractFile(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "contract", name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return data
}

func readVectors(t *testing.T, name string) []contractVector {
	t.Helper()
	var vectors []contractVector
	if err := json.Unmarshal(readContractFile(t, name), &vectors); err != nil {
		t.Fatalf("decode %s: %v", name, err)
	}
	if len(vectors) == 0 {
		t.Fatalf("%s has no vectors", name)
	}
	return vectors
}

func (v contractVector) bytes() []byte {
	if v.Raw != "" {
		return []byte(v.Raw)
	}
	return v.Answer
}

func validDeclaration(t *testing.T) *Declaration {
	t.Helper()
	declaration, err := DecodeDeclaration(readContractFile(t, "declaration.valid.json"))
	if err != nil {
		t.Fatalf("valid declaration refused: %v", err)
	}
	return declaration
}

func TestDeclarationContract_AcceptsTheStudioCatalogAndRefusesEveryInvalidVector(t *testing.T) {
	declaration := validDeclaration(t)
	if declaration.ID != "reaper-studio" || declaration.BlueprintID != "reaper-studio" || len(declaration.Categories) != 3 {
		t.Fatalf("declaration = %+v", declaration)
	}
	templates, ok := declaration.Category("templates")
	if !ok || templates.Facts == nil || len(templates.Facts) != 0 {
		t.Fatalf("templates category = %+v", templates)
	}
	// What the host stores must decode again under the same strict rule.
	stored, err := json.Marshal(declaration)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeDeclaration(stored); err != nil {
		t.Fatalf("stored declaration no longer decodes: %v\n%s", err, stored)
	}
	for _, vector := range readVectors(t, "declaration.invalid.json") {
		t.Run(vector.Name, func(t *testing.T) {
			_, err := DecodeDeclaration(vector.bytes())
			if !errors.Is(err, ErrInvalidDeclaration) {
				t.Fatalf("err = %v, want ErrInvalidDeclaration", err)
			}
			t.Logf("refused: %v", err)
		})
	}
}

func TestSourcesContract_AcceptsTheValidAnswerAndRefusesEveryInvalidVector(t *testing.T) {
	declaration := validDeclaration(t)
	answer, err := DecodeSourcesAnswer(readContractFile(t, "sources.valid.json"), declaration)
	if err != nil {
		t.Fatalf("valid sources refused: %v", err)
	}
	if answer.Installation.ID != "inst_8f3c2a91d4e7b605" || len(answer.Sources) != 5 {
		t.Fatalf("answer = %+v", answer)
	}
	live, ok := answer.Source("audio_plugins")
	if !ok || live.Access != AccessLive || live.State != SourceAppClosed {
		t.Fatalf("audio plugins source = %+v", live)
	}
	for _, vector := range readVectors(t, "sources.invalid.json") {
		t.Run(vector.Name, func(t *testing.T) {
			_, err := DecodeSourcesAnswer(vector.bytes(), declaration)
			if !errors.Is(err, ErrInvalidAnswer) {
				t.Fatalf("err = %v, want ErrInvalidAnswer", err)
			}
			t.Logf("refused: %v", err)
		})
	}
}

func TestSourcesContract_RefusesAnAnswerOverItsByteLimit(t *testing.T) {
	declaration := validDeclaration(t)
	padded := append(readContractFile(t, "sources.valid.json"), []byte(strings.Repeat(" ", MaxSourcesAnswerBytes))...)
	if _, err := DecodeSourcesAnswer(padded, declaration); !errors.Is(err, ErrInvalidAnswer) {
		t.Fatalf("err = %v, want ErrInvalidAnswer", err)
	}
}

func inventoryFixture(t *testing.T) (*Declaration, Source, InventoryRequest) {
	t.Helper()
	declaration := validDeclaration(t)
	sources, err := DecodeSourcesAnswer(readContractFile(t, "sources.valid.json"), declaration)
	if err != nil {
		t.Fatal(err)
	}
	source, _ := sources.Source("reaper_scripts")
	var request InventoryRequest
	if err := json.Unmarshal(readContractFile(t, "inventory.request.valid.json"), &request); err != nil {
		t.Fatal(err)
	}
	built, err := NewInventoryRequest(request.InstallationID, request.SourceID, request.Cursor, request.Limit, nil)
	if err != nil || built != request {
		t.Fatalf("NewInventoryRequest = %+v, %v; fixture %+v", built, err, request)
	}
	return declaration, source, request
}

func TestInventoryContract_AcceptsTheValidPageAndRefusesEveryInvalidVector(t *testing.T) {
	declaration, source, request := inventoryFixture(t)
	answer, err := DecodeInventoryAnswer(readContractFile(t, "inventory.valid.json"), declaration, source, request)
	if err != nil {
		t.Fatalf("valid page refused: %v", err)
	}
	if len(answer.Entries) != 3 || *answer.NextCursor == "" || len(answer.Coverage.Skipped) != 2 {
		t.Fatalf("answer = %+v", answer)
	}
	// Two scripts with the same name stay two entries.
	first := EntryID(request.InstallationID, request.SourceID, answer.Entries[0].NativeID)
	second := EntryID(request.InstallationID, request.SourceID, answer.Entries[1].NativeID)
	if answer.Entries[0].Name != answer.Entries[1].Name || first == second {
		t.Fatalf("same-name entries collapsed: %s %s", first, second)
	}
	for _, vector := range readVectors(t, "inventory.invalid.json") {
		t.Run(vector.Name, func(t *testing.T) {
			_, err := DecodeInventoryAnswer(vector.bytes(), declaration, source, request)
			if !errors.Is(err, ErrInvalidAnswer) {
				t.Fatalf("err = %v, want ErrInvalidAnswer", err)
			}
			t.Logf("refused: %v", err)
		})
	}
}

func TestInventoryContract_RefusesMoreEntriesThanTheRequestAskedFor(t *testing.T) {
	declaration, source, request := inventoryFixture(t)
	request.Limit = 2
	if _, err := DecodeInventoryAnswer(readContractFile(t, "inventory.valid.json"), declaration, source, request); !errors.Is(err, ErrInvalidAnswer) {
		t.Fatalf("err = %v, want ErrInvalidAnswer", err)
	}
}

func TestNewInventoryRequest_RefusesWhatThePluginMustNeverBeAsked(t *testing.T) {
	cases := map[string]InventoryRequest{
		"foreign installation id": {InstallationID: "../etc", SourceID: "reaper_scripts", Limit: 1},
		"path as source":          {InstallationID: "inst_abc123", SourceID: "/Users/me", Limit: 1},
		"cursor with a newline":   {InstallationID: "inst_abc123", SourceID: "reaper_scripts", Cursor: "a\nb", Limit: 1},
		"zero limit":              {InstallationID: "inst_abc123", SourceID: "reaper_scripts", Limit: 0},
		"oversized limit":         {InstallationID: "inst_abc123", SourceID: "reaper_scripts", Limit: MaxPageEntries + 1},
	}
	folder := &FolderRef{Category: "templates", Path: "/Users/me/Templates Archive"}
	folderID := FolderID(folder.Category, folder.Path)
	cases["folder without its folder"] = InventoryRequest{InstallationID: "inst_abc123", SourceID: folderID, Limit: 1}
	cases["folder named for another source"] = InventoryRequest{InstallationID: "inst_abc123", SourceID: "reaper_scripts", Limit: 1, Folder: folder}
	cases["folder that is not its id's"] = InventoryRequest{InstallationID: "inst_abc123", SourceID: folderID, Limit: 1,
		Folder: &FolderRef{Category: "scripts", Path: folder.Path}}
	cases["relative folder"] = InventoryRequest{InstallationID: "inst_abc123", SourceID: FolderID("templates", "Templates"), Limit: 1,
		Folder: &FolderRef{Category: "templates", Path: "Templates"}}
	cases["unclean folder"] = InventoryRequest{InstallationID: "inst_abc123", SourceID: FolderID("templates", "/Users/me/../me"), Limit: 1,
		Folder: &FolderRef{Category: "templates", Path: "/Users/me/../me"}}
	for name, request := range cases {
		if _, err := NewInventoryRequest(request.InstallationID, request.SourceID, request.Cursor, request.Limit, request.Folder); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := NewInventoryRequest("inst_abc123", folderID, "", 1, folder); err != nil {
		t.Fatalf("an owner folder's page was refused: %v", err)
	}
}

// An owner folder travels as the vectors show: in the sources input, back as
// one file source, and named again on each of its inventory pages.
func TestOwnerFolderContract_MatchesTheVectors(t *testing.T) {
	declaration := validDeclaration(t)
	folder := OwnerFolder{Category: "templates", Path: "/Users/me/Music/Templates Archive"}
	folder.ID = FolderID(folder.Category, folder.Path)
	input, sent := sourcesInput(declaration, []OwnerFolder{folder, {ID: FolderID("audio_plugins", "/Users/me"), Category: "audio_plugins", Path: "/Users/me"}})
	var got, want any
	if err := json.Unmarshal(input, &got); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(readContractFile(t, "sources.request.valid.json"), &want); err != nil {
		t.Fatal(err)
	}
	if !jsonEqual(got, want) || len(sent) != 1 {
		t.Fatalf("sources input = %s (sent %d); a category without owner_folders must not be sent", input, len(sent))
	}
	answer, err := DecodeSourcesAnswer(readContractFile(t, "sources.folder.valid.json"), declaration)
	if err != nil {
		t.Fatal(err)
	}
	if err := checkFolderSources(answer, sent); err != nil {
		t.Fatalf("the folder answer was refused: %v", err)
	}
	if err := checkFolderSources(answer, nil); err == nil {
		t.Fatal("a folder that was never sent was accepted")
	}
	plain, err := DecodeSourcesAnswer(readContractFile(t, "sources.valid.json"), declaration)
	if err != nil {
		t.Fatal(err)
	}
	if err := checkFolderSources(plain, sent); err == nil {
		t.Fatal("an answer that left the folder out was accepted")
	}
	var request InventoryRequest
	if err := json.Unmarshal(readContractFile(t, "inventory.request.folder.valid.json"), &request); err != nil {
		t.Fatal(err)
	}
	built, err := NewInventoryRequest(request.InstallationID, folder.ID, "", PageLimit, &FolderRef{Category: folder.Category, Path: folder.Path})
	if err != nil || built.SourceID != request.SourceID || *built.Folder != *request.Folder {
		t.Fatalf("folder page = %+v, %v; vector %+v", built, err, request)
	}
}

func jsonEqual(left, right any) bool {
	a, _ := json.Marshal(left)
	b, _ := json.Marshal(right)
	return string(a) == string(b)
}

// The operation declarations a provider copies into its manifest must accept
// every valid vector, so the host's schema check and its strict decoder agree.
func TestOperationSchemas_AcceptTheContractVectors(t *testing.T) {
	var declared []struct {
		ID           string          `json:"id"`
		InputSchema  json.RawMessage `json:"input_schema"`
		OutputSchema json.RawMessage `json:"output_schema"`
	}
	if err := json.Unmarshal(readContractFile(t, "operations.json"), &declared); err != nil {
		t.Fatal(err)
	}
	operations := map[string]workspacesurface.Operation{}
	for _, operation := range declared {
		operations[operation.ID] = workspacesurface.Operation{ID: operation.ID, InputSchema: operation.InputSchema, OutputSchema: operation.OutputSchema}
	}
	sources, inventory := operations["studio.sources.read"], operations["studio.inventory.read"]
	if err := workspacesurface.ValidateOperationInput(sources, json.RawMessage(`{"schema_version":1}`)); err != nil {
		t.Fatalf("sources input: %v", err)
	}
	if err := workspacesurface.ValidateOperationInput(sources, readContractFile(t, "sources.request.valid.json")); err != nil {
		t.Fatalf("sources input with folders: %v", err)
	}
	if err := workspacesurface.ValidateOperationInput(inventory, readContractFile(t, "inventory.request.folder.valid.json")); err != nil {
		t.Fatalf("inventory input for a folder: %v", err)
	}
	if err := workspacesurface.ValidateOperationOutput(sources, readContractFile(t, "sources.valid.json")); err != nil {
		t.Fatalf("sources output: %v", err)
	}
	if err := workspacesurface.ValidateOperationOutput(sources, readContractFile(t, "sources.folder.valid.json")); err != nil {
		t.Fatalf("sources output with a folder: %v", err)
	}
	if err := workspacesurface.ValidateOperationInput(inventory, readContractFile(t, "inventory.request.valid.json")); err != nil {
		t.Fatalf("inventory input: %v", err)
	}
	if err := workspacesurface.ValidateOperationOutput(inventory, readContractFile(t, "inventory.valid.json")); err != nil {
		t.Fatalf("inventory output: %v", err)
	}
	if err := workspacesurface.ValidateOperationInput(inventory, json.RawMessage(`{"schema_version":1,"installation_id":"inst_abc123","source_id":"x","cursor":"","limit":257}`)); err == nil {
		t.Fatal("the inventory schema accepts a page larger than the contract")
	}
}

func TestEntryID_BindsInstallationSourceAndNativeID(t *testing.T) {
	base := EntryID("inst_abc123", "reaper_scripts", "a.lua")
	if !strings.HasPrefix(base, "rc_") || len(base) != 3+32 {
		t.Fatalf("entry id = %q", base)
	}
	if base != EntryID("inst_abc123", "reaper_scripts", "a.lua") {
		t.Fatal("entry id is not deterministic")
	}
	for _, other := range []string{
		EntryID("inst_def456", "reaper_scripts", "a.lua"),
		EntryID("inst_abc123", "ori_scripts", "a.lua"),
		EntryID("inst_abc123", "reaper_scripts", "b.lua"),
		// The separator keeps the parts from sliding into one another.
		EntryID("inst_abc123", "reaper_script", "sa.lua"),
	} {
		if other == base {
			t.Fatalf("different identity produced the same entry id %s", base)
		}
	}
}
