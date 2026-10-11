package mcp

import (
	"context"
	"sort"
)

// CapabilityMetadata is a configuration/status observation, not proof a server
// is installed, connected, granted to the assistant, or operationally verified.
// No command, args, URL, credential/ref, environment or tool schema is returned.
type CapabilityMetadata struct {
	Name      string       `json:"name"`
	Transport string       `json:"transport"`
	Enabled   bool         `json:"enabled"`
	Status    ServerStatus `json:"status"`
}

type CapabilityInventory struct {
	State     string               `json:"state"`
	Servers   []CapabilityMetadata `json:"servers"`
	Truncated bool                 `json:"truncated"`
}

func (r *Registry) CapabilityInventory(ctx context.Context, limit int) CapabilityInventory {
	result := CapabilityInventory{State: "empty", Servers: []CapabilityMetadata{}}
	if r == nil || ctx.Err() != nil {
		result.State = "unavailable"
		return result
	}
	if limit <= 0 || limit > 200 {
		limit = 200
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, server := range r.servers {
		if ctx.Err() != nil {
			return CapabilityInventory{State: "unavailable", Servers: []CapabilityMetadata{}}
		}
		if len(result.Servers) == limit {
			result.Truncated = true
			break
		}
		server.mu.RLock()
		result.Servers = append(result.Servers, CapabilityMetadata{
			Name: server.config.Name, Transport: NormalizedTransport(server.config), Enabled: server.config.Enabled, Status: server.status,
		})
		server.mu.RUnlock()
	}
	sort.Slice(result.Servers, func(i, j int) bool { return result.Servers[i].Name < result.Servers[j].Name })
	if len(result.Servers) > 0 {
		result.State = "available"
	}
	if result.Truncated {
		result.State = "partial"
	}
	return result
}
