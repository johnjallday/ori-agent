package assistantdiscovery

import (
	"context"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/mcp"
	"github.com/johnjallday/ori-agent/internal/mcp/mcpregistry"
	"github.com/johnjallday/ori-agent/internal/skills"
)

type countedSkillOwner struct {
	SkillInventoryReader
	calls int
}

func (o *countedSkillOwner) PersonalSkillInventory(ctx context.Context, limit int) skills.SkillInventory {
	o.calls++
	return o.SkillInventoryReader.PersonalSkillInventory(ctx, limit)
}

type countedMCPOwner struct {
	MCPInventoryReader
	calls int
}

func (o *countedMCPOwner) CapabilityInventory(ctx context.Context, limit int) mcp.CapabilityInventory {
	o.calls++
	return o.MCPInventoryReader.CapabilityInventory(ctx, limit)
}

type countedCatalogOwner struct {
	MCPCatalogReader
	calls int
}

func (o *countedCatalogOwner) SearchCached(ctx context.Context, query mcpregistry.SearchQuery) mcpregistry.CachedSearchResult {
	o.calls++
	return o.MCPCatalogReader.SearchCached(ctx, query)
}

func TestDiscoveryColdWarmReadsStayLazyBoundedAndUseOnlyRequestedOwners(t *testing.T) {
	fixture, _ := discoveryFixture(t)
	skills := &countedSkillOwner{SkillInventoryReader: fixture.skills}
	servers := &countedMCPOwner{MCPInventoryReader: fixture.servers}
	catalog := &countedCatalogOwner{MCPCatalogReader: fixture.registry}
	service := NewService(skills, servers, catalog, noExternalCatalog{t})
	if skills.calls+servers.calls+catalog.calls != 0 {
		t.Fatal("construction scanned source owners")
	}
	for i, phase := range []string{"cold", "warm"} {
		started := time.Now()
		installed := service.Installed(context.Background(), "telegram")
		if installed.Availability != Available || skills.calls != i+1 || servers.calls != i+1 || catalog.calls != i {
			t.Fatalf("%s inventory performed unrelated reads", phase)
		}
		listing := service.MCPCatalog(context.Background(), "filesystem")
		if len(listing.Candidates) == 0 || len(listing.Candidates) > MaxCandidates || catalog.calls != i+1 || skills.calls != i+1 || servers.calls != i+1 {
			t.Fatalf("%s catalog performed unrelated/unbounded reads", phase)
		}
		t.Logf("%s requested metadata only: %s; one call per requested owner; no model/HTTP/install", phase, time.Since(started))
	}
}
