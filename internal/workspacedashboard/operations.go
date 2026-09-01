package workspacedashboard

import (
	"encoding/json"
	"strconv"

	"github.com/johnjallday/ori-agent/internal/workspacesurface"
)

// The v1 read-only operation vocabulary.
//
// This list is fixed, not an allowlist over a generic query path. That is the
// central design decision: the complete set of data a dashboard can read is
// reviewable in one file, and widening it is a visible change rather than a
// configuration edit.
const (
	OpWorkspaceSummary = "workspace.summary"
	OpTasksList        = "workspace.tasks.list"
	OpNotesList        = "workspace.notes.list"
	OpAgentsList       = "workspace.agents.list"
	OpSessionsList     = "workspace.sessions.list"
	OpFilesList        = "workspace.files.list"
)

// Response bounds. The bridge caps a message at 64 KiB with a depth limit of 16
// and 256 entries per level (workspace-surface-bridge.js), and a message over
// the cap is dropped silently by the sender — so a response that exceeds it does
// not fail loudly, it just never arrives. Every list is therefore bounded here
// rather than relying on the data happening to be small.
const (
	// maxListLimit is the most entries any list operation will return in one
	// call, comfortably under the bridge's 256-per-level ceiling.
	maxListLimit = 100
	// defaultListLimit applies when the dashboard does not ask for a size.
	defaultListLimit = 25
	// maxOutputBytes leaves room for the bridge envelope inside the 64 KiB cap.
	maxOutputBytes = 48 << 10
	// maxTextBytes truncates any single free-text field (a task title, a note
	// name) so one pathological record cannot consume the whole budget.
	maxTextBytes = 500
)

/*
The secret boundary (FR18).

No operation reads a secret, and none filters one out either — the difference
matters. Filtering leaves the secret one forgotten field away from exposure;
never gathering it means there is nothing to forget. Concretely:

  - Vault contents and provider credentials live in internal/vault and the
    settings store. This package imports neither, and nothing it does import
    reaches them.
  - Agent system prompts and per-workspace prompt refinements
    (AgentInstance.CustomInstructions, and the shared agent definition behind
    Name) are never copied into a response. workspace.agents.list returns
    identity and role only.
  - Task free text beyond the title — Details, Context, Result,
    StructuredResult, Error, ExecutionTrace — is excluded. Those carry whatever
    an agent produced or a tool returned, which is exactly where a credential
    echoed into a task result would sit.
  - Note bodies are excluded; workspace.notes.list returns names only. The
    session store's own note summary carries a Preview field, and this package
    deliberately drops it rather than pass a body excerpt through.
  - Session transcripts are excluded; workspace.sessions.list returns a title,
    an agent name, and a timestamp.
  - workspace.files.list returns names, sizes, and modification times from the
    workspace folder. It never reads file contents.

Anything not listed in the response builders in runtime.go is not gathered. A
future operation that needs a new field must add it there, in the open.
*/

// operationIDs is the fixed set of operations a dashboard surface declares.
func operationIDs() []string {
	return []string{
		OpWorkspaceSummary,
		OpTasksList,
		OpNotesList,
		OpAgentsList,
		OpSessionsList,
		OpFilesList,
	}
}

// listInputSchema is shared by every list operation: an optional page size and
// offset, nothing else. additionalProperties is false, so a dashboard cannot
// smuggle an unexpected field — notably a workspace id — past validation.
func listInputSchema(extraProperties string) json.RawMessage {
	properties := `"limit":{"type":"integer","minimum":1,"maximum":100},` +
		`"offset":{"type":"integer","minimum":0}`
	if extraProperties != "" {
		properties += "," + extraProperties
	}
	return json.RawMessage(`{"type":"object","properties":{` + properties + `},"required":[],"additionalProperties":false}`)
}

// Output schemas are fully explicit. The host's validator is deliberately
// stricter than JSON Schema — every object must close additionalProperties and
// declare each key, every array needs items and maxItems, every string needs
// maxLength — so a response can never carry an undeclared field. That makes
// these schemas the enforced contract for what a dashboard receives, not just
// documentation of it.
func listOutputSchema(collection, entry string) json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{` +
		`"` + collection + `":{"type":"array","maxItems":` + strconv.Itoa(maxListLimit) + `,"items":` + entry + `},` +
		`"total":{"type":"integer","minimum":0},` +
		`"limit":{"type":"integer","minimum":0},` +
		`"offset":{"type":"integer","minimum":0},` +
		`"has_more":{"type":"boolean"}` +
		`},"required":["` + collection + `"],"additionalProperties":false}`)
}

// text is a bounded string field. maxLength is counted in runes by the
// validator and clip() truncates by bytes, so a clipped value always fits.
const text = `{"type":"string","maxLength":` + maxTextBytesText + `}`

const (
	maxTextBytesText = "500"

	taskEntrySchema = `{"type":"object","properties":{` +
		`"id":` + text + `,"title":` + text + `,"status":` + text + `,` +
		`"priority":{"type":"integer","minimum":-1000,"maximum":1000},` +
		`"assignee":` + text +
		`},"required":["id","title","status"],"additionalProperties":false}`

	noteEntrySchema = `{"type":"object","properties":{` +
		`"id":` + text + `,"name":` + text +
		`},"required":["id","name"],"additionalProperties":false}`

	agentEntrySchema = `{"type":"object","properties":{` +
		`"id":` + text + `,"name":` + text + `,"role":` + text + `,` +
		`"instance_number":{"type":"integer","minimum":0},` +
		`"entry_point":{"type":"boolean"}` +
		`},"required":["id","name"],"additionalProperties":false}`

	sessionEntrySchema = `{"type":"object","properties":{` +
		`"title":` + text + `,"agent_name":` + text + `,"updated_at":` + text +
		`},"required":["title"],"additionalProperties":false}`

	fileEntrySchema = `{"type":"object","properties":{` +
		`"name":` + text + `,"size":{"type":"integer","minimum":0},` +
		`"is_dir":{"type":"boolean"},"modified_at":` + text +
		`},"required":["name","size"],"additionalProperties":false}`

	countsSchema = `{"type":"object","properties":{` +
		`"tasks":{"type":"integer","minimum":0},` +
		`"open_tasks":{"type":"integer","minimum":0},` +
		`"agents":{"type":"integer","minimum":0},` +
		`"notes":{"type":"integer","minimum":0},` +
		`"sessions":{"type":"integer","minimum":0}` +
		`},"required":[],"additionalProperties":false}`
)

func readOnly(id string, input, output json.RawMessage) workspacesurface.Operation {
	return workspacesurface.Operation{
		ID: id, InputSchema: input, OutputSchema: output,
		MaxOutputBytes: maxOutputBytes,
		// Every operation reads local state already in memory or one directory
		// listing away. None of them waits on a network or a subprocess.
		Timeout: workspacesurface.TimeoutFast,
		// PolicyReadOnly is load-bearing, not decorative: it is what keeps these
		// operations out of the confirmation path, and what a reviewer checks to
		// see that a dashboard cannot change anything.
		Policy: workspacesurface.PolicyReadOnly,
	}
}

// operations declares the trusted policy for each id in operationIDs. The two
// must agree exactly: ValidateRegistration rejects a binding whose operation set
// does not match its descriptor.
func operations() map[string]workspacesurface.Operation {
	empty := json.RawMessage(`{"type":"object","properties":{},"required":[],"additionalProperties":false}`)
	return map[string]workspacesurface.Operation{
		OpWorkspaceSummary: readOnly(OpWorkspaceSummary, empty,
			json.RawMessage(`{"type":"object","properties":{`+
				`"id":`+text+`,"name":`+text+`,"kind":`+text+`,`+
				`"designation":`+text+`,"description":`+text+`,`+
				`"tags":{"type":"array","maxItems":64,"items":`+text+`},`+
				`"counts":`+countsSchema+
				`},"required":["id","name","counts"],"additionalProperties":false}`)),
		OpTasksList: readOnly(OpTasksList,
			listInputSchema(`"status":{"type":"string","maxLength":32}`),
			listOutputSchema("tasks", taskEntrySchema)),
		OpNotesList:    readOnly(OpNotesList, listInputSchema(""), listOutputSchema("notes", noteEntrySchema)),
		OpAgentsList:   readOnly(OpAgentsList, listInputSchema(""), listOutputSchema("agents", agentEntrySchema)),
		OpSessionsList: readOnly(OpSessionsList, listInputSchema(""), listOutputSchema("sessions", sessionEntrySchema)),
		OpFilesList:    readOnly(OpFilesList, listInputSchema(""), listOutputSchema("files", fileEntrySchema)),
	}
}
