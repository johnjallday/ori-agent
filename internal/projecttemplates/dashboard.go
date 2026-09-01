package projecttemplates

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// DashboardDirName is the directory a template may carry to ship a custom
// workspace dashboard. Its contents are copied to
// `<workspace folder>/.ori/dashboard/` when a workspace is created from the
// template, so the workspace opens with the dashboard already attached.
//
// The template holds it under a plain visible name rather than the `.ori`
// sidecar path it lands in: a template folder is something a person authors and
// browses, and a hidden directory inside it would be needlessly hard to find.
// The mapping to `.ori/dashboard/` happens here, once.
const DashboardDirName = "dashboard"

// ErrDashboardExists reports that the workspace folder already has a dashboard.
// Installing never overwrites one: if a workspace has a dashboard, whoever put
// it there wins over the template.
var ErrDashboardExists = errors.New("workspace already has a dashboard")

// maxTemplateDashboardEntries bounds the copy. A template dashboard is a handful
// of small files; anything past this is a mistake, and refusing beats writing
// thousands of files into a new workspace.
const maxTemplateDashboardEntries = 512

// HasDashboard reports whether the template ships a custom dashboard — that is,
// whether `<template>/dashboard/index.html` exists and is a regular file. The
// entry file is required: a dashboard directory without one produces no surface
// in the workspace, so shipping it would silently do nothing.
func HasDashboard(templatePath string) bool {
	templatePath = strings.TrimSpace(templatePath)
	if templatePath == "" {
		return false
	}
	entry := filepath.Join(templatePath, DashboardDirName, workspace.CustomDashboardEntryAsset)
	info, err := os.Lstat(entry)
	return err == nil && info.Mode().IsRegular()
}

// InstallDashboard copies a template's dashboard into a workspace folder,
// creating `<workspaceFolder>/.ori/dashboard/`.
//
// It reports false with a nil error when the template ships no dashboard, which
// is the common case and not a failure. An existing dashboard in the workspace
// is left alone (ErrDashboardExists) rather than overwritten.
//
// Like the template skeleton copy, symlinks are skipped: copying the pointer
// would smuggle in machine-local absolute paths, and following it could pull in
// files from outside the template.
func InstallDashboard(templatePath, workspaceFolder string) (bool, error) {
	if !HasDashboard(templatePath) {
		return false, nil
	}
	workspaceFolder = strings.TrimSpace(workspaceFolder)
	if workspaceFolder == "" {
		return false, fmt.Errorf("workspace folder is required to install a dashboard")
	}
	if info, err := os.Stat(workspaceFolder); err != nil || !info.IsDir() {
		return false, fmt.Errorf("workspace folder %q is not accessible", workspaceFolder)
	}

	source := filepath.Join(templatePath, DashboardDirName)
	destRoot := filepath.Join(workspaceFolder, workspace.SidecarDirName, workspace.CustomDashboardDirName)
	if _, err := os.Lstat(destRoot); err == nil {
		return false, fmt.Errorf("%w: %s", ErrDashboardExists, destRoot)
	} else if !os.IsNotExist(err) {
		return false, fmt.Errorf("failed to inspect %q: %w", destRoot, err)
	}

	if err := copyDashboardTree(source, destRoot); err != nil {
		// A half-copied dashboard would render as a broken one. Leave nothing.
		_ = os.RemoveAll(destRoot)
		return false, err
	}
	return true, nil
}

func copyDashboardTree(source, destRoot string) error {
	if err := os.MkdirAll(destRoot, 0o750); err != nil {
		return fmt.Errorf("failed to create dashboard folder: %w", err)
	}
	entries := 0
	return fs.WalkDir(os.DirFS(source), ".", func(relPath string, d fs.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("failed to read dashboard entry %q: %w", relPath, err)
		}
		if relPath == "." {
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		entries++
		if entries > maxTemplateDashboardEntries {
			return fmt.Errorf("template dashboard has more than %d files", maxTemplateDashboardEntries)
		}

		destPath := filepath.Join(destRoot, filepath.FromSlash(relPath))
		if !strings.HasPrefix(destPath, destRoot+string(filepath.Separator)) {
			return fmt.Errorf("dashboard entry %q escapes the dashboard folder", relPath)
		}
		info, err := d.Info()
		if err != nil {
			return fmt.Errorf("failed to inspect dashboard entry %q: %w", relPath, err)
		}
		if d.IsDir() {
			return os.MkdirAll(destPath, normalizeDirPerm(info.Mode().Perm()))
		}
		return copyFile(filepath.Join(source, filepath.FromSlash(relPath)), destPath, info.Mode().Perm())
	})
}
