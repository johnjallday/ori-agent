package workspacearrangement

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/johnjallday/ori-agent/internal/toolapi"
)

// Invocation contains model input only. Identity and capabilities are never
// accepted here; Evidence closes over the host-authorized execution context.
type Invocation struct {
	ID, Name, Arguments string
}

type ToolResult struct {
	ID, Name, Content string
}

// ToolSession is deliberately batch-only: a generic sequential tool dispatcher
// must not execute an innocent first call before discovering a forbidden tail.
// It has no persistence or approval callback.
type ToolSession struct {
	mu           sync.Mutex
	evidence     *Evidence
	calls, bytes int
	terminal     bool
	ids          map[string]bool
}

func NewToolSession(evidence *Evidence) *ToolSession {
	return &ToolSession{evidence: evidence, ids: map[string]bool{}}
}

func schemaObject(properties map[string]any, required ...string) map[string]any {
	if required == nil {
		required = []string{}
	}
	return map[string]any{"type": "object", "additionalProperties": false, "properties": properties, "required": required}
}
func schemaString(maximum int) map[string]any {
	return map[string]any{"type": "string", "maxLength": maximum}
}
func schemaArray(item any, maximum int) map[string]any {
	return map[string]any{"type": "array", "items": item, "maxItems": maximum}
}

// ToolDefinitions returns fresh schemas; a provider cannot mutate another turn's
// allowlist. Runtime checks remain authoritative, including byte rather than
// character limits and source-specific operation support.
func ToolDefinitions() []toolapi.ToolDefinition {
	filter := schemaObject(map[string]any{"text": schemaString(120), "format": schemaString(80), "connection": schemaString(80),
		"availability": schemaString(80), "root_id": schemaString(160), "collection_id": schemaString(160), "selection_handle": schemaString(160)})
	selection := schemaObject(map[string]any{"ids": schemaArray(schemaString(160), MaxExplicitItems), "handle": schemaString(160)})
	// Live models tend to send a handle and the ids it stands for together; the
	// contract takes one or the other, and a refused draft ends the turn.
	selection["description"] = "Name items in exactly one way: a frozen selection handle from arrangement_select, or up to 100 explicit ids copied from issued rows. Never both. An empty selection is {}."
	existingCollection := schemaString(160)
	existingCollection["description"] = "Only for " + RenameCollection + ", " + SetMembers + " and " + RemoveCollection + ": the existing collection's id, with collection_revision. Leave unset for " + CreateCollection + ", " + CreateGroup + " and " + SelectActivation + "."
	operation := schemaObject(map[string]any{
		"kind": map[string]any{"type": "string", "enum": []string{CreateCollection, RenameCollection, SetMembers, RemoveCollection, CreateGroup, SelectActivation}},
		"name": schemaString(128), "collection_id": existingCollection, "collection_revision": map[string]any{"type": "integer", "minimum": 1}, "members": selection,
	}, "kind", "members")
	proposal := schemaObject(map[string]any{
		"version": map[string]any{"type": "integer", "enum": []int{Version}}, "source_revision": schemaString(160),
		"goal": schemaString(2000), "rationale": schemaString(2000), "unknowns": schemaArray(schemaString(500), 16),
		"unchanged": selection, "needs_review": schemaArray(schemaObject(map[string]any{"items": selection, "reason": schemaString(500)}, "items", "reason"), 32),
		"operations": schemaArray(operation, MaxOperations),
	}, "version", "source_revision", "goal", "rationale", "unknowns", "unchanged", "needs_review", "operations")
	return []toolapi.ToolDefinition{
		{Name: "arrangement_overview", Description: "Read held/saved metadata counts and at most five nonrepresentative examples. Optional literal collection-name filter and collection cursor page existing collections, five at a time. No scan, content read or action.", Parameters: schemaObject(map[string]any{"collection_text": schemaString(120), "collection_cursor": schemaString(8192)})},
		{Name: "arrangement_search", Description: "Page already-held metadata with literal AND filters; no expressions, globs or new discovery. Optional selection_handle restricts results to a previously issued frozen set from this turn. At most 20 rows. Preserve the returned cursor and filter together.", Parameters: schemaObject(map[string]any{"filter": filter, "cursor": schemaString(8192), "page_size": map[string]any{"type": "integer", "minimum": 0, "maximum": ModelPageSize}})},
		{Name: "arrangement_detail", Description: "Read bounded metadata for one previously issued item ID. Tree nodes are observations, not creator targets.", Parameters: schemaObject(map[string]any{"id": schemaString(160)}, "id")},
		{Name: "arrangement_select", Description: "Freeze the exact current set matching literal filters; returns a handle/count/digest, not approval or a future-match rule. Empty matches issue no handle.", Parameters: schemaObject(map[string]any{"filter": filter})},
		{Name: "arrangement_propose", Description: "Submit inert intent as the sole call in its batch. This does not save, approve or execute anything. Unmentioned items stay unchanged; use needs_review for unsupported effects. Attachment supports plain create_group; Home supports logical collections and selected activation for later independent review. Each selection (members, unchanged, needs_review items) takes a frozen handle OR explicit ids, never both; collection_id and collection_revision belong only to operations on an existing collection. A draft that breaks these rules is refused whole.", Parameters: proposal},
	}
}

type queryArguments struct {
	Filter   Filter `json:"filter"`
	Cursor   string `json:"cursor"`
	PageSize int    `json:"page_size"`
}

type overviewArguments struct {
	Text   string `json:"collection_text"`
	Cursor string `json:"collection_cursor"`
}

type preparedCall struct {
	Invocation
	overview overviewArguments
	query    queryArguments
	itemID   string
}

func strictArguments(raw string, target any) error {
	if len(raw) > MaxDraftBytes || !utf8.ValidString(raw) || !strings.HasPrefix(strings.TrimSpace(raw), "{") {
		return ErrInvalid
	}
	if uniqueJSON(json.NewDecoder(strings.NewReader(raw)), 0) != nil {
		return ErrInvalid
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(target) != nil || decoder.Decode(new(any)) != io.EOF {
		return ErrInvalid
	}
	return nil
}

func prepareCall(call Invocation) (preparedCall, error) {
	p := preparedCall{Invocation: call}
	if !label(call.ID, 160) || len(call.Arguments) > MaxDraftBytes {
		return p, ErrInvalid
	}
	if call.Name != "arrangement_propose" {
		// Null is not an empty filter: accepting it could broaden a malformed
		// selection into the entire inventory. Strings containing "null" are fine.
		decoder := json.NewDecoder(strings.NewReader(call.Arguments))
		for {
			token, err := decoder.Token()
			if err == io.EOF {
				break
			}
			if err != nil || token == nil {
				return p, ErrInvalid
			}
		}
	}
	var err error
	switch call.Name {
	case "arrangement_overview":
		err = strictArguments(call.Arguments, &p.overview)
	case "arrangement_search":
		err = strictArguments(call.Arguments, &p.query)
		if p.query.PageSize < 0 || p.query.PageSize > ModelPageSize || len(p.query.Cursor) > 8192 {
			return p, ErrInvalid
		}
	case "arrangement_select":
		var args struct {
			Filter Filter `json:"filter"`
		}
		err = strictArguments(call.Arguments, &args)
		p.query.Filter = args.Filter
	case "arrangement_detail":
		var args struct {
			ID string `json:"id"`
		}
		err = strictArguments(call.Arguments, &args)
		p.itemID = args.ID
		if !label(p.itemID, 160) {
			return p, ErrInvalid
		}
	case "arrangement_propose":
		// Full source/selection validation happens in Resolve, before returning
		// the terminal intent. No preceding call is allowed in that batch.
		var draft Draft
		err = strictArguments(call.Arguments, &draft)
	default:
		err = ErrInvalid
	}
	return p, err
}

func (s *ToolSession) preflight(ctx context.Context, calls []preparedCall) error {
	e := s.evidence
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.check(ctx); err != nil {
		return err
	}
	for _, call := range calls {
		switch call.Name {
		case "arrangement_overview":
			if _, _, _, err := e.collectionPage(call.overview.Text, call.overview.Cursor); err != nil {
				return err
			}
		case "arrangement_search", "arrangement_select":
			rows, err := e.matching(call.query.Filter)
			if err != nil {
				return err
			}
			if call.Name == "arrangement_search" {
				if _, err := e.offset(call.query.Cursor, call.query.Filter, rows); err != nil {
					return err
				}
			}
		case "arrangement_detail":
			if !e.issued[call.itemID] {
				return ErrInvalid
			}
		}
	}
	return nil
}

// Execute validates the whole batch before any query, enforces cumulative tool
// and sharing budgets, and returns a resolved terminal draft only to the host.
// A caller must revalidate again before its canonical atomic turn save.
func (s *ToolSession) Execute(ctx context.Context, calls []Invocation) ([]ToolResult, *Draft, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.evidence == nil || s.terminal {
		return nil, nil, ErrUnavailable
	}
	if len(calls) == 0 || len(calls) > MaxToolCalls-s.calls {
		return nil, nil, ErrLimit
	}
	prepared := make([]preparedCall, 0, len(calls))
	ids := map[string]bool{}
	for _, call := range calls {
		p, err := prepareCall(call)
		if err != nil || ids[call.ID] || s.ids[call.ID] || (call.Name == "arrangement_propose" && len(calls) != 1) {
			return nil, nil, ErrInvalid
		}
		ids[call.ID] = true
		prepared = append(prepared, p)
	}
	if err := s.preflight(ctx, prepared); err != nil {
		return nil, nil, err
	}
	// A partially evaluated batch is never reusable after an error: references
	// prepared before a later budget/source failure were not delivered to the
	// model. Only a wholly successful nonterminal batch reopens the session.
	s.terminal = true
	s.calls += len(calls)
	for id := range ids {
		s.ids[id] = true
	}
	results := make([]ToolResult, 0, len(calls))
	var draft *Draft
	for _, call := range prepared {
		var value any
		var err error
		switch call.Name {
		case "arrangement_overview":
			value, err = s.evidence.OverviewPage(ctx, call.overview.Text, call.overview.Cursor)
		case "arrangement_search":
			value, err = s.evidence.Search(ctx, call.query.Filter, call.query.Cursor, call.query.PageSize)
		case "arrangement_detail":
			value, err = s.evidence.Detail(ctx, call.itemID)
		case "arrangement_select":
			value, err = s.evidence.Select(ctx, call.query.Filter)
		case "arrangement_propose":
			var resolved Draft
			resolved, err = s.evidence.Resolve(ctx, call.Arguments)
			if err == nil {
				draft = &resolved
			}
			value = map[string]string{"status": "validated_not_saved", "notice": "Inert intent only; no approval or effects."}
		}
		if err != nil {
			return nil, nil, err
		}
		encoded, err := json.Marshal(value)
		if err != nil || len(encoded) > MaxToolResultBytes || len(encoded) > MaxTurnEvidenceBytes-s.bytes {
			return nil, nil, ErrLimit
		}
		s.bytes += len(encoded)
		results = append(results, ToolResult{ID: call.ID, Name: call.Name, Content: string(encoded)})
	}
	if err := s.evidence.Revalidate(ctx); err != nil {
		return nil, nil, err
	}
	s.terminal = draft != nil
	return results, draft, nil
}
