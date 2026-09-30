package plugin

import "errors"

// ErrHomeUpgradeRequired is the replacement guard's refusal to replace a
// package that existing Homes are pinned to. The owner moves those Homes
// through the reviewed Home package upgrade on the Home page instead
// (docs/architecture/independent-program-homes.md §6.1).
var ErrHomeUpgradeRequired = errors.New("plugin replacement would strand existing Home; reviewed Home/child upgrade is required first")

// HomeUpgradeRequiredError names the Home where the owner reviews the upgrade.
// HomePath is that Home's page route; it carries no workspace ID.
type HomeUpgradeRequiredError struct {
	HomeName string
	HomePath string
}

func (e *HomeUpgradeRequiredError) Error() string { return ErrHomeUpgradeRequired.Error() }

func (e *HomeUpgradeRequiredError) Is(target error) bool { return target == ErrHomeUpgradeRequired }
