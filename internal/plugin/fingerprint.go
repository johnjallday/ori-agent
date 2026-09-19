package plugin

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
)

type trustedBlueprintFingerprint struct {
	ID       string `json:"id"`
	Version  int    `json:"version"`
	Skeleton string `json:"skeleton"`
}

type trustedSkillFingerprint struct {
	Name   string `json:"name"`
	Digest string `json:"digest"`
}

type trustedFingerprintInput struct {
	MCPServers        []MCPServerSpec               `json:"mcp_servers,omitempty"`
	Skills            []trustedSkillFingerprint     `json:"skills,omitempty"`
	WorkspaceSurfaces *SurfaceContribution          `json:"workspace_surfaces,omitempty"`
	Blueprints        []trustedBlueprintFingerprint `json:"blueprints,omitempty"`
	AssetDigest       string                        `json:"asset_digest,omitempty"`
}

// trustedComponentFingerprint changes whenever executable/runtime footprint or
// requested access changes. It intentionally stores only the digest in
// installed.json; trust previews carry the human-readable diff surface.
func trustedComponentFingerprint(descriptor PluginDescriptor) string {
	input := trustedFingerprintInput{
		MCPServers:        append([]MCPServerSpec(nil), descriptor.MCPServers...),
		WorkspaceSurfaces: descriptor.WorkspaceSurfaces,
		AssetDigest:       descriptor.TrustedAssetDigest,
	}
	for _, skill := range descriptor.Skills {
		digest, err := SkillTreeDigest(skill.Path)
		if err != nil {
			// Registration will reject the same unsafe or unreadable tree. Keep
			// the fingerprint deterministically invalid in case a caller compares
			// the descriptor before registration reaches that check.
			digest = "unavailable"
		}
		input.Skills = append(input.Skills, trustedSkillFingerprint{Name: skill.Name, Digest: digest})
	}
	for _, blueprint := range descriptor.ResolvedBlueprints {
		input.Blueprints = append(input.Blueprints, trustedBlueprintFingerprint{
			ID: blueprint.ID, Version: blueprint.Version, Skeleton: blueprint.SkeletonDigest,
		})
	}
	sort.Slice(input.MCPServers, func(i, j int) bool { return input.MCPServers[i].Name < input.MCPServers[j].Name })
	sort.Slice(input.Skills, func(i, j int) bool { return input.Skills[i].Name < input.Skills[j].Name })
	sort.Slice(input.Blueprints, func(i, j int) bool { return input.Blueprints[i].ID < input.Blueprints[j].ID })
	data, err := json.Marshal(input)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}
