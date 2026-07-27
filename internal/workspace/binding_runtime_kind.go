package workspace

import (
	"errors"
	"fmt"
	"strings"
)

// Runtime classification for workspace bindings.
//
// Not every workspace binding is an MCP server. Email is a NATIVE Ori
// capability: a workspace binding named `gmail` authorizes Ori's own mailbox
// tools and has no MCP server template behind it. The runtime used to discover
// this the hard way — it looked up an MCP template named "gmail", correctly
// failed to find one, and blocked the whole task with "server gmail not found".
//
// RuntimeKind makes the distinction explicit and checkable BEFORE any template
// lookup happens. This file is the single source of that classification: both
// the runtime resolver and the mailbox access gate consult it, so the two can
// never drift into disagreeing about what a binding is (FR 31).

// BindingRuntimeKind is how a workspace binding is realized at runtime.
type BindingRuntimeKind string

const (
	// RuntimeKindMCP is a binding backed by a configured MCP server template that
	// can be materialized as a runtime MCP server.
	RuntimeKindMCP BindingRuntimeKind = "mcp"
	// RuntimeKindNativeEmail is a binding that authorizes Ori's built-in mailbox
	// tools for an email account. It has no MCP server and must never be looked
	// up as one.
	RuntimeKindNativeEmail BindingRuntimeKind = "native_email"
)

// ErrUnknownRuntimeKind means a binding declares a runtime kind this build does
// not understand. Classification fails closed rather than guessing: guessing
// "mcp" would try to launch a server for an unknown capability, and guessing
// "native_email" would hand out mailbox access (FR 23).
var ErrUnknownRuntimeKind = errors.New("workspace: unknown binding runtime kind")

// nativeEmailServerNames are the binding names that have always meant "native
// mailbox" in Ori. They predate RuntimeKind, so bindings carrying no explicit
// kind are classified by name for backward compatibility (FR 22, 23).
var nativeEmailServerNames = map[string]struct{}{
	"email":          {},
	"gmail":          {},
	"microsoft-mail": {},
	"microsoft":      {},
	"outlook-mail":   {},
	"imap-smtp":      {},
	"imap_smtp":      {},
}

// IsNativeEmailServerName reports whether a binding server name is one of the
// recognized native-mailbox aliases. Matching is case- and whitespace-
// insensitive because these names arrive from templates, the API, and hand-
// edited workspace.json alike.
func IsNativeEmailServerName(serverName string) bool {
	_, ok := nativeEmailServerNames[strings.ToLower(strings.TrimSpace(serverName))]
	return ok
}

// EffectiveRuntimeKind classifies a binding, resolving the legacy case where no
// kind was recorded:
//
//   - an explicit `mcp` or `native_email` is honored as written;
//   - an explicit but unrecognized kind fails closed with ErrUnknownRuntimeKind;
//   - no explicit kind falls back to the server name: recognized email aliases
//     are native, everything else is MCP.
func (b MCPBinding) EffectiveRuntimeKind() (BindingRuntimeKind, error) {
	switch declared := BindingRuntimeKind(strings.ToLower(strings.TrimSpace(string(b.RuntimeKind)))); declared {
	case RuntimeKindMCP, RuntimeKindNativeEmail:
		return declared, nil
	case "":
		if IsNativeEmailServerName(b.ServerName) {
			return RuntimeKindNativeEmail, nil
		}
		return RuntimeKindMCP, nil
	default:
		return "", fmt.Errorf("%w: %q (binding %s)", ErrUnknownRuntimeKind, b.RuntimeKind, b.ID)
	}
}

// IsNativeEmail reports whether this binding is a native mailbox binding. A
// binding whose kind cannot be classified is NOT native — the caller that needs
// to materialize it will fail closed on the same error.
func (b MCPBinding) IsNativeEmail() bool {
	kind, err := b.EffectiveRuntimeKind()
	return err == nil && kind == RuntimeKindNativeEmail
}

// validateRuntimeKind rejects a runtime kind this build does not understand at
// the API boundary, so an unusable binding is never persisted in the first
// place. Empty is accepted — it means "classify me by server name".
func validateRuntimeKind(kind BindingRuntimeKind) error {
	switch BindingRuntimeKind(strings.ToLower(strings.TrimSpace(string(kind)))) {
	case "", RuntimeKindMCP, RuntimeKindNativeEmail:
		return nil
	default:
		return fmt.Errorf("invalid runtime_kind %q: expected %q or %q", kind, RuntimeKindMCP, RuntimeKindNativeEmail)
	}
}

// IsRuntimeMCP reports whether this binding may be looked up and materialized as
// an MCP server. Everything that is not a *classifiable* MCP binding — native
// email, and any unknown kind — answers false, which is what keeps a native
// binding out of MCP template lookup (FR 24, 28).
func (b MCPBinding) IsRuntimeMCP() bool {
	kind, err := b.EffectiveRuntimeKind()
	return err == nil && kind == RuntimeKindMCP
}
