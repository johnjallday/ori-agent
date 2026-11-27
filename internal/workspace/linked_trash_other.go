//go:build !darwin

package workspace

import "os"

func trashLinkedItem(*os.Root, string, SourceVersion, bool) error { return ErrSourceUnreadable }
func moveLinkedItem(*os.Root, string, *os.Root, SourceVersion, bool) error {
	return ErrSourceUnreadable
}
