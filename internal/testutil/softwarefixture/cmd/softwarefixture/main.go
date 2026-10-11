// Command softwarefixture writes the disposable software-project fixtures to a
// directory and prints where each one is, as JSON. Browser suites and demo
// scripts use it so they share the trees the Go tests use.
//
// Usage: go run ./internal/testutil/softwarefixture/cmd/softwarefixture <empty-dir>
//
// With "hash <dir>" it prints the digest of a folder's names and bytes instead,
// for checking that a setup or an assessment left a project untouched.
package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/johnjallday/ori-agent/internal/testutil/softwarefixture"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "softwarefixture:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 2 && args[0] == "hash" {
		digest, err := softwarefixture.Hash(args[1])
		if err != nil {
			return err
		}
		fmt.Println(digest)
		return nil
	}
	if len(args) != 1 {
		return fmt.Errorf("usage: softwarefixture <empty-dir> | softwarefixture hash <dir>")
	}
	root := args[0]
	if err := os.MkdirAll(root, 0o750); err != nil {
		return err
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	if len(entries) != 0 {
		return fmt.Errorf("%s is not empty; fixtures are only written into a new directory", root)
	}
	set, err := softwarefixture.Build(root)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(set)
}
