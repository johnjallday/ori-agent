// Command vaultfixture writes the disposable notes folders to a directory and
// prints where each one is, with the facts a correct reader finds in it, as
// JSON. Browser suites and demo scripts use it so they share the folders the
// Go tests use.
//
// Usage: go run ./internal/testutil/vaultfixture/cmd/vaultfixture [-flavours music,software,research] [-home DIR] <empty-dir>
//
// With -home it also writes Obsidian's vault list where Obsidian keeps it on
// this system under that home folder, naming the Obsidian vaults it built.
// Point -home at a sandbox, never at a real home folder.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/secondbrain"
	"github.com/johnjallday/ori-agent/internal/testutil/vaultfixture"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "vaultfixture:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	flags := flag.NewFlagSet("vaultfixture", flag.ContinueOnError)
	flavours := flags.String("flavours", strings.Join(vaultfixture.Flavours, ","), "comma-separated flavours to build")
	home := flags.String("home", "", "also write Obsidian's vault list under this home folder")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 1 {
		return fmt.Errorf("usage: vaultfixture [-flavours list] [-home dir] <empty-dir>")
	}
	// A developer tool: the directory is the operator's own command-line
	// argument, and nothing is written unless it is empty.
	root := flags.Arg(0)
	if err := os.MkdirAll(root, 0o750); err != nil { // #nosec G703 -- the operator's own argument to a fixture tool
		return err
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	if len(entries) != 0 {
		return fmt.Errorf("%s is not empty; fixtures are only written into a new directory", root)
	}

	now := time.Now()
	var vaults []vaultfixture.Vault
	for index, flavour := range strings.Split(*flavours, ",") {
		vault, err := vaultfixture.Build(root, strings.TrimSpace(flavour), now)
		if err != nil {
			return err
		}
		vault.LastOpened = now.Add(-time.Duration(index+1) * time.Hour).Truncate(time.Millisecond)
		vaults = append(vaults, vault)
	}

	output := struct {
		Vaults    []vaultfixture.Vault `json:"vaults"`
		VaultList string               `json:"vault_list,omitempty"`
	}{Vaults: vaults}
	if *home != "" {
		output.VaultList = secondbrain.VaultListPath(runtime.GOOS, *home, "")
		if err := vaultfixture.WriteVaultList(output.VaultList, vaults...); err != nil {
			return err
		}
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(output)
}
