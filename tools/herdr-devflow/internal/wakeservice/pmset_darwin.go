//go:build darwin

package wakeservice

import (
	"context"
	"os/exec"
)

const pmsetExecutable = "/usr/bin/pmset"

func defaultPMSetRunner(ctx context.Context, arguments []string) ([]byte, error) {
	// #nosec G204 -- the executable is fixed and callers can only reach the
	// three fixed argument shapes constructed by PMSet.
	command := exec.CommandContext(ctx, pmsetExecutable, arguments...)
	return command.CombinedOutput()
}
