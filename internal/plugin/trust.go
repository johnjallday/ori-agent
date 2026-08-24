package plugin

import (
	"fmt"
	"sort"
	"strings"
)

// TrustReport enumerates everything a plugin will register or expose, shown to
// the user before install (PRD req #15). Install proceeds only on explicit
// confirmation; declining makes no changes.
type TrustReport struct {
	Name                string
	Format              SourceFormat
	MCPCommands         []string // "<namespaced server> → <resolved command>"
	Skills              []string
	SurfaceCapabilities []string
	Surfaces            []SurfaceDisclosure
	Services            []ServiceDisclosure
	Operations          []OperationDisclosure
	Artifacts           []ArtifactDisclosure
	SymbolicScopes      []string
	Blueprints          []string
	Unsupported         []UnsupportedComponent
	Warnings            []string // e.g. binary-missing
}

type SurfaceDisclosure struct {
	Capability string
	ID         string
	Label      string
	EntryAsset string
	BrowserUI  bool
}

type ServiceDisclosure struct {
	ID         string
	Transport  string
	Executable string
	Platforms  []string
}

type OperationDisclosure struct {
	Service string
	ID      string
	Policy  string
	Timeout string
	Scopes  []string
}

type ArtifactDisclosure struct {
	Service  string
	ID       string
	Platform string
	Source   string
	SHA256   string
	Size     int64
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
	if contribution := d.WorkspaceSurfaces; contribution != nil {
		for _, capability := range contribution.Capabilities {
			r.SurfaceCapabilities = append(r.SurfaceCapabilities, capability.ID+" — "+capability.Display.Name)
			for _, surface := range capability.Surfaces {
				r.Surfaces = append(r.Surfaces, SurfaceDisclosure{
					Capability: capability.ID, ID: surface.ID, Label: surface.Label,
					EntryAsset: surface.EntryAsset, BrowserUI: true,
				})
			}
		}
		for _, service := range contribution.Services {
			platforms := make([]string, 0, len(service.Artifacts))
			for _, artifact := range service.Artifacts {
				platform := artifact.OS + "/" + artifact.Arch
				platforms = append(platforms, platform)
				source := artifact.Source.URL
				if source == "" {
					source = artifact.Source.Path
				}
				r.Artifacts = append(r.Artifacts, ArtifactDisclosure{
					Service: service.ID, ID: artifact.ID, Platform: platform,
					Source: source, SHA256: artifact.SHA256, Size: artifact.Size,
				})
			}
			r.Services = append(r.Services, ServiceDisclosure{
				ID: service.ID, Transport: service.Transport,
				Executable: service.Entrypoint.ArtifactID + " " + strings.Join(service.Entrypoint.Args, " "),
				Platforms:  platforms,
			})
			for _, operation := range service.Operations {
				r.Operations = append(r.Operations, OperationDisclosure{
					Service: service.ID, ID: operation.ID, Policy: operation.Policy,
					Timeout: operation.TimeoutClass, Scopes: append([]string(nil), operation.Scopes...),
				})
				r.SymbolicScopes = append(r.SymbolicScopes, operation.Scopes...)
			}
		}
		for _, blueprint := range contribution.Blueprints {
			r.Blueprints = append(r.Blueprints, blueprint.ID)
		}
		r.SymbolicScopes = uniqueSorted(r.SymbolicScopes)
	}
	return r
}

// String renders a human-readable disclosure (used by CLIs and tests).
func uniqueSorted(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

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
	if len(r.Surfaces) > 0 {
		b.WriteString("  Sandboxed browser surfaces (executable UI code):\n")
		for _, surface := range r.Surfaces {
			fmt.Fprintf(&b, "    - %s/%s — %s (%s)\n", surface.Capability, surface.ID, surface.Label, surface.EntryAsset)
		}
	}
	if len(r.Services) > 0 {
		b.WriteString("  Native plugin services (trusted local code execution):\n")
		for _, service := range r.Services {
			fmt.Fprintf(&b, "    - %s → %s via %s [%s]\n", service.ID, service.Executable, service.Transport, strings.Join(service.Platforms, ", "))
		}
	}
	if len(r.Operations) > 0 {
		b.WriteString("  Declared operations:\n")
		for _, operation := range r.Operations {
			fmt.Fprintf(&b, "    - %s/%s — %s, %s", operation.Service, operation.ID, operation.Policy, operation.Timeout)
			if len(operation.Scopes) > 0 {
				fmt.Fprintf(&b, ", scopes: %s", strings.Join(operation.Scopes, ", "))
			}
			b.WriteByte('\n')
		}
	}
	if len(r.Artifacts) > 0 {
		b.WriteString("  Verified platform artifacts:\n")
		for _, artifact := range r.Artifacts {
			fmt.Fprintf(&b, "    - %s/%s — %s, sha256 %s, %d bytes\n", artifact.Service, artifact.ID, artifact.Platform, artifact.SHA256, artifact.Size)
		}
	}
	if len(r.Blueprints) > 0 {
		fmt.Fprintf(&b, "  Workspace blueprints: %s\n", strings.Join(r.Blueprints, ", "))
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
