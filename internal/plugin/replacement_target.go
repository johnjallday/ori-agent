package plugin

import "fmt"

// ReplacementTarget is one inspected, not installed, candidate for replacing an
// installed plugin: the descriptor the replacement would register, its trust
// disclosure, and the trusted component fingerprint the replacement guard will
// be asked about. A reviewed Home package upgrade binds its plan to exactly
// this version and fingerprint before anything is installed.
type ReplacementTarget struct {
	Descriptor  PluginDescriptor
	Report      TrustReport
	Fingerprint string
}

// InspectReplacementTarget resolves an explicit replacement source — the
// counterpart of UpdateFromSource — without installing or registering anything.
func (m *Manager) InspectReplacementTarget(name, source string, prefer SourceFormat) (ReplacementTarget, error) {
	m.operationMu.Lock()
	defer m.operationMu.Unlock()
	if _, ok, err := m.store.Get(name); err != nil {
		return ReplacementTarget{}, err
	} else if !ok {
		return ReplacementTarget{}, fmt.Errorf("plugin: %q not installed", name)
	}
	descriptor, report, err := m.inspect(source, prefer)
	if err != nil {
		return ReplacementTarget{}, err
	}
	if descriptor.Name != name {
		return ReplacementTarget{}, fmt.Errorf("plugin: reviewed replacement identity mismatch")
	}
	return ReplacementTarget{Descriptor: descriptor, Report: report, Fingerprint: trustedComponentFingerprint(descriptor)}, nil
}

// InspectUpdateTarget re-resolves an installed plugin from its recorded
// source — the counterpart of Update — without installing or registering
// anything.
func (m *Manager) InspectUpdateTarget(name string) (ReplacementTarget, error) {
	m.operationMu.Lock()
	defer m.operationMu.Unlock()
	existing, ok, err := m.store.Get(name)
	if err != nil {
		return ReplacementTarget{}, err
	}
	if !ok {
		return ReplacementTarget{}, fmt.Errorf("plugin: %q not installed", name)
	}
	descriptor, err := m.previewReload(existing)
	if err != nil {
		return ReplacementTarget{}, err
	}
	if err := prepareTrustedBlueprints(&descriptor); err != nil {
		return ReplacementTarget{}, err
	}
	if descriptor.Name != name {
		return ReplacementTarget{}, fmt.Errorf("plugin: recorded source now names another plugin")
	}
	return ReplacementTarget{Descriptor: descriptor, Report: BuildTrustReport(descriptor), Fingerprint: trustedComponentFingerprint(descriptor)}, nil
}
