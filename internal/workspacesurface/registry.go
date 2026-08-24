package workspacesurface

import (
	"fmt"
	"sort"
	"strings"
	"sync"
)

// Registry is the process-wide owner-aware catalog of trusted Workspace Surface
// contributions. Registration is atomic: validation/collision failure publishes
// neither descriptors nor executable bindings.
type Registry struct {
	mu       sync.RWMutex
	owners   map[string]Owner
	surfaces map[string]registered
	order    []string
}

type registered struct {
	public  RegisteredSurface
	binding Binding
}

func NewRegistry() *Registry {
	return &Registry{
		owners:   make(map[string]Owner),
		surfaces: make(map[string]registered),
	}
}

// RegisterTrusted publishes one already-verified installed contribution. Only
// plugin lifecycle/server wiring should call it in production; manifests and
// workspace files never receive a Registry reference.
func (r *Registry) RegisterTrusted(registration Registration) error {
	if r == nil {
		return fmt.Errorf("workspace surface registry is not initialized")
	}
	if err := validateRegistration(registration); err != nil {
		return err
	}

	owner := cloneOwner(registration.Owner)
	owner.ProtocolMax = normalizeProtocolMax(owner)
	ownerKey := owner.key()

	capabilities := make(map[string]Capability, len(registration.Capabilities))
	for _, capability := range registration.Capabilities {
		capabilities[capability.ID] = cloneCapability(capability)
	}
	bindings := make(map[string]Binding, len(registration.Bindings))
	for _, binding := range registration.Bindings {
		bindings[binding.CapabilityID+"\x00"+binding.SurfaceID] = cloneBinding(binding)
	}

	pending := make(map[string]registered)
	var pendingOrder []string
	for _, originalCapability := range registration.Capabilities {
		capability := capabilities[originalCapability.ID]
		for _, surface := range capability.Surfaces {
			key := SurfaceKey(owner, capability.ID, surface.ID)
			pending[key] = registered{
				public: RegisteredSurface{
					Key:        key,
					Owner:      owner,
					Capability: cloneCapabilityWithoutSiblingSurfaces(capability, surface),
					Surface:    cloneSurface(surface),
				},
				binding: cloneBinding(bindings[capability.ID+"\x00"+surface.ID]),
			}
			pendingOrder = append(pendingOrder, key)
		}
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if existing, collision := r.owners[ownerKey]; collision {
		return fmt.Errorf("workspace surface owner %q is already registered at generation %d", ownerKey, existing.Generation)
	}
	for key := range pending {
		if _, collision := r.surfaces[key]; collision {
			return fmt.Errorf("workspace surface key %q is already registered", key)
		}
	}

	r.owners[ownerKey] = owner
	for _, key := range pendingOrder {
		r.surfaces[key] = pending[key]
		r.order = append(r.order, key)
	}
	return nil
}

// UnregisterOwner atomically removes one exact owner generation. Requiring the
// generation keeps a delayed disable/update cleanup from unregistering a newer
// contribution that has already replaced it.
func (r *Registry) UnregisterOwner(kind OwnerKind, id string, generation uint64) error {
	if r == nil {
		return fmt.Errorf("workspace surface registry is not initialized")
	}
	owner := Owner{Kind: kind, ID: strings.TrimSpace(id)}
	ownerKey := owner.key()

	r.mu.Lock()
	defer r.mu.Unlock()
	existing, ok := r.owners[ownerKey]
	if !ok {
		return fmt.Errorf("workspace surface owner %q is not registered", ownerKey)
	}
	if generation == 0 || existing.Generation != generation {
		return fmt.Errorf("workspace surface owner %q generation does not match", ownerKey)
	}

	delete(r.owners, ownerKey)
	kept := r.order[:0]
	for _, key := range r.order {
		entry := r.surfaces[key]
		if entry.public.Owner.key() == ownerKey && entry.public.Owner.Generation == generation {
			delete(r.surfaces, key)
			continue
		}
		kept = append(kept, key)
	}
	if len(kept) == 0 {
		r.order = nil
	} else {
		r.order = kept
	}
	return nil
}

// Surface returns inert public metadata for a stable qualified surface key.
func (r *Registry) Surface(key string) (RegisteredSurface, bool) {
	if r == nil {
		return RegisteredSurface{}, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	entry, ok := r.surfaces[strings.TrimSpace(key)]
	if !ok {
		return RegisteredSurface{}, false
	}
	return cloneRegisteredSurface(entry.public), true
}

// Binding returns executable trust only to host packages. It deliberately uses
// the same exact qualified key as Surface so policy cannot resolve one owner and
// execute another owner's runtime.
func (r *Registry) Binding(key string) (Binding, bool) {
	if r == nil {
		return Binding{}, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	entry, ok := r.surfaces[strings.TrimSpace(key)]
	if !ok {
		return Binding{}, false
	}
	return cloneBinding(entry.binding), true
}

// Surfaces returns all inert descriptors in deterministic registration order.
func (r *Registry) Surfaces() []RegisteredSurface {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]RegisteredSurface, 0, len(r.order))
	for _, key := range r.order {
		out = append(out, cloneRegisteredSurface(r.surfaces[key].public))
	}
	return out
}

// SurfacesForOwner returns one owner's inert descriptors sorted by qualified
// key, useful for deterministic install/API projections.
func (r *Registry) SurfacesForOwner(kind OwnerKind, id string) []RegisteredSurface {
	if r == nil {
		return nil
	}
	ownerKey := string(kind) + ":" + strings.TrimSpace(id)
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []RegisteredSurface
	for _, entry := range r.surfaces {
		if entry.public.Owner.key() == ownerKey {
			out = append(out, cloneRegisteredSurface(entry.public))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

func cloneRegisteredSurface(surface RegisteredSurface) RegisteredSurface {
	copy := surface
	copy.Owner = cloneOwner(surface.Owner)
	copy.Capability = cloneCapability(surface.Capability)
	copy.Surface = cloneSurface(surface.Surface)
	return copy
}

// A resolved surface carries the selected surface only. Keeping sibling
// surfaces out prevents accidental catalog duplication and makes mutation of a
// returned descriptor unable to affect another registry entry.
func cloneCapabilityWithoutSiblingSurfaces(capability Capability, surface Surface) Capability {
	copy := cloneCapability(capability)
	copy.Surfaces = []Surface{cloneSurface(surface)}
	return copy
}
