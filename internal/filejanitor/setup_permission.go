package filejanitor

// HasSetupPermission reads only the saved grant. Unlike Status it performs no
// persistence health probe, directory creation, filesystem scan or automation.
func (s *Service) HasSetupPermission(workspaceID string) (bool, error) {
	if s == nil || s.store == nil {
		return false, ErrInvalidSettings
	}
	settings, err := s.store.LoadSettings(workspaceID)
	if err != nil {
		return false, err
	}
	return settings.IsSetUp(), nil
}
