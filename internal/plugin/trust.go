package plugin

import (
	"fmt"
	"strings"
)

// TrustReport enumerates everything a plugin will register or expose, shown to
// the user before install (PRD req #15). Install proceeds only on explicit
// confirmation; declining makes no changes.
type TrustReport struct {
	Name        string
	Format      SourceFormat
	MCPCommands []string // "<namespaced server> → <resolved command>"
	Skills      []string
	Unsupported []UnsupportedComponent
	Warnings    []string // e.g. binary-missing
}

// ConfirmFunc returns true to proceed with installation. It receives the report
// so a CLI/UI can render the disclosure (supplied by the HTTP/UI layer).
type ConfirmFunc func(TrustReport) bool

// BuildTrustReport produces the install disclosure for a descriptor, resolving
// the exact commands each MCP server will run and flagging missing binaries.
func BuildTrustReport(d PluginDescriptor) TrustReport {
	r := TrustReport{Name: d.Name, Format: d.SourceFormat, Unsupported: d.Unsupported}
	for _, spec := range d.MCPServers {
		cmd, args := resolveCommand(spec, d.InstallDir)
		full := cmd
		if len(args) > 0 {
			full = cmd + " " + strings.Join(args, " ")
		}
		name := NamespacedServerName(d.Name, spec.Name)
		r.MCPCommands = append(r.MCPCommands, fmt.Sprintf("%s → %s", name, full))
		if !CommandAvailable(cmd) {
			r.Warnings = append(r.Warnings, fmt.Sprintf("%s: command not found — install the binary, then enable", name))
		}
	}
	for _, s := range d.Skills {
		r.Skills = append(r.Skills, s.Name)
	}
	return r
}

// String renders a human-readable disclosure (used by CLIs and tests).
func (r TrustReport) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Plugin %q (%s) will register:\n", r.Name, r.Format)
	if len(r.MCPCommands) > 0 {
		b.WriteString("  MCP servers (run as local commands):\n")
		for _, c := range r.MCPCommands {
			fmt.Fprintf(&b, "    - %s\n", c)
		}
	}
	if len(r.Skills) > 0 {
		fmt.Fprintf(&b, "  Skills: %s\n", strings.Join(r.Skills, ", "))
	}
	if len(r.Unsupported) > 0 {
		b.WriteString("  Skipped (not yet supported):\n")
		for _, u := range r.Unsupported {
			fmt.Fprintf(&b, "    - %s: %s\n", u.Kind, u.Detail)
		}
	}
	for _, w := range r.Warnings {
		fmt.Fprintf(&b, "  ! %s\n", w)
	}
	return b.String()
}
