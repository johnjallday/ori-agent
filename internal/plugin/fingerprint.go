package plugin

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
)

type trustedFingerprintInput struct {
	MCPServers        []MCPServerSpec      `json:"mcp_servers,omitempty"`
	Skills            []string             `json:"skills,omitempty"`
	WorkspaceSurfaces *SurfaceContribution `json:"workspace_surfaces,omitempty"`
}

// trustedComponentFingerprint changes whenever executable/runtime footprint or
// requested access changes. It intentionally stores only the digest in
// installed.json; trust previews carry the human-readable diff surface.
func trustedComponentFingerprint(descriptor PluginDescriptor) string {
	input := trustedFingerprintInput{
		MCPServers:        append([]MCPServerSpec(nil), descriptor.MCPServers...),
		WorkspaceSurfaces: descriptor.WorkspaceSurfaces,
	}
	for _, skill := range descriptor.Skills {
		input.Skills = append(input.Skills, skill.Name)
	}
	sort.Slice(input.MCPServers, func(i, j int) bool { return input.MCPServers[i].Name < input.MCPServers[j].Name })
	sort.Strings(input.Skills)
	data, err := json.Marshal(input)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}
