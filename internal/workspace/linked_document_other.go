//go:build !darwin

package workspace

// Production does not issue or dispatch desktop selections outside macOS.
// Portable contract tests inject a recording opener and exercise the common
// regular-file and identity checks without Finder metadata.
func ordinaryDesktopDocument(string, SourceVersion) bool { return true }
