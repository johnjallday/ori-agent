// Command herdr-wake is the dedicated privileged wake daemon. Keep this
// command's dependency graph narrow: it must never import the Ori server,
// Herdr agent control, model providers, Git, worktrees, browsers, or
// system-sleep packages.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/wakeinstall"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/wakeservice"
)

var buildVersion = "dev"

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	os.Exit(run(
		ctx,
		os.Args[1:],
		os.Stdout,
		os.Stderr,
		wakeservice.ServeDefault,
		wakeinstall.InstallDefault,
		wakeinstall.UninstallDefault,
	))
}

type serveFunc func(context.Context, string) error
type installFunc func(context.Context, int, string, string, string) error
type uninstallFunc func(context.Context, int) error

func run(
	ctx context.Context,
	args []string,
	stdout, stderr io.Writer,
	serve serveFunc,
	install installFunc,
	uninstall uninstallFunc,
) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "herdr-wake: expected serve or a typed root lifecycle command")
		return 2
	}
	if len(args) == 1 && (args[0] == "--help" || args[0] == "-h") {
		fmt.Fprintln(stdout, "Usage: herdr-wake serve")
		fmt.Fprintln(stdout, "       herdr-wake install --allowed-uid UID --artifact-digest SHA256 --build BUILD")
		fmt.Fprintln(stdout, "       herdr-wake uninstall --allowed-uid UID")
		fmt.Fprintln(stdout, "Dedicated root LaunchDaemon for verified Herdr-owned macOS wake events.")
		return 0
	}
	var err error
	switch args[0] {
	case "serve":
		if len(args) != 1 {
			fmt.Fprintln(stderr, "herdr-wake: serve accepts no options")
			return 2
		}
		err = serve(ctx, buildVersion)
	case "install":
		uid, digest, requestedBuild, parseErr := parseInstallArgs(args[1:])
		if parseErr != nil {
			fmt.Fprintf(stderr, "herdr-wake: %v\n", parseErr)
			return 2
		}
		err = install(ctx, uid, digest, requestedBuild, buildVersion)
	case "uninstall":
		uid, parseErr := parseUninstallArgs(args[1:])
		if parseErr != nil {
			fmt.Fprintf(stderr, "herdr-wake: %v\n", parseErr)
			return 2
		}
		err = uninstall(ctx, uid)
	default:
		fmt.Fprintf(stderr, "herdr-wake: unknown command %q\n", args[0])
		return 2
	}
	if err != nil {
		switch {
		case errors.Is(err, wakeservice.ErrUnsupported):
			fmt.Fprintln(stderr, "herdr-wake: unsupported: macOS 12 or newer is required")
		case errors.Is(err, wakeservice.ErrRequiresRoot):
			fmt.Fprintln(stderr, "herdr-wake: refused: the daemon must be started by the system LaunchDaemon")
		case errors.Is(err, wakeinstall.ErrInstallationUncertain):
			fmt.Fprintln(stderr, "herdr-wake: uncertain: installation may own a wake; run wt herd wake doctor")
		default:
			fmt.Fprintf(stderr, "herdr-wake: failed: %v\n", err)
		}
		return 1
	}
	return 0
}

func parseInstallArgs(args []string) (int, string, string, error) {
	if len(args) != 6 ||
		args[0] != "--allowed-uid" ||
		args[2] != "--artifact-digest" ||
		args[4] != "--build" {
		return 0, "", "", fmt.Errorf("install requires the fixed --allowed-uid, --artifact-digest, and --build arguments")
	}
	uid, err := strconv.Atoi(args[1])
	if err != nil || uid <= 0 {
		return 0, "", "", fmt.Errorf("--allowed-uid must be a non-root numeric uid")
	}
	if len(args[3]) != 64 {
		return 0, "", "", fmt.Errorf("--artifact-digest must be a SHA-256 hex digest")
	}
	for _, character := range args[3] {
		if !((character >= '0' && character <= '9') || (character >= 'a' && character <= 'f')) {
			return 0, "", "", fmt.Errorf("--artifact-digest must be lowercase hexadecimal")
		}
	}
	if args[5] == "" {
		return 0, "", "", fmt.Errorf("--build is required")
	}
	return uid, args[3], args[5], nil
}

func parseUninstallArgs(args []string) (int, error) {
	if len(args) != 2 || args[0] != "--allowed-uid" {
		return 0, fmt.Errorf("uninstall requires exactly --allowed-uid UID")
	}
	uid, err := strconv.Atoi(args[1])
	if err != nil || uid <= 0 {
		return 0, fmt.Errorf("--allowed-uid must be a non-root numeric uid")
	}
	return uid, nil
}
