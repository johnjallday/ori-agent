package folderdigest

import (
	"os"
	"path/filepath"
	"strings"
)

// InstalledApp is one tool-table application whose bundle was found in an
// Applications folder.
type InstalledApp struct {
	// ToolID and Name are the tool row's identity and display name.
	ToolID string
	Name   string
}

// systemApplicationsDir is where applications are installed for every user.
const systemApplicationsDir = "/Applications"

// applicationsDirEnv replaces the system Applications folder, so a sandboxed
// server reports the applications its fixture holds instead of this
// computer's. The per-user folder still follows the home directory.
const applicationsDirEnv = "ORI_APPLICATIONS_DIR"

// DetectInstalledApps reports which tool-table applications are installed, in
// table order. It looks for each row's bundle names directly inside the system
// Applications folder and the user's own. A bundle counts only when it is a
// real directory: a symbolic link is not followed, and nothing inside a bundle
// is read. The table decides what is looked for; nothing here names an
// application.
func DetectInstalledApps(homeDir string) []InstalledApp {
	system := systemApplicationsDir
	if override := strings.TrimSpace(os.Getenv(applicationsDirEnv)); override != "" && filepath.IsAbs(override) {
		system = override
	}
	dirs := []string{system}
	if homeDir = strings.TrimSpace(homeDir); homeDir != "" && filepath.IsAbs(homeDir) {
		dirs = append(dirs, filepath.Join(homeDir, "Applications"))
	}
	return detectInstalledApps(dirs)
}

func detectInstalledApps(dirs []string) []InstalledApp {
	var found []InstalledApp
	seen := make(map[string]bool)
	for _, tool := range Tools {
		if tool.ToolID == "" || seen[tool.ToolID] || !toolBundleInstalled(tool, dirs) {
			continue
		}
		seen[tool.ToolID] = true
		found = append(found, InstalledApp{ToolID: tool.ToolID, Name: tool.ToolName})
	}
	return found
}

func toolBundleInstalled(tool Tool, dirs []string) bool {
	for _, bundle := range tool.AppBundles {
		// A bundle name is one path element; anything else is a table mistake
		// and is never joined onto a folder.
		if bundle == "" || bundle != filepath.Base(bundle) {
			continue
		}
		for _, dir := range dirs {
			if bundleInDir(dir, bundle) {
				return true
			}
		}
	}
	return false
}

func bundleInDir(dir, bundle string) bool {
	if !strings.Contains(bundle, "*") {
		return realDirectory(filepath.Join(dir, bundle))
	}
	// A versioned bundle needs the folder's own listing; entries are matched by
	// name and nothing below the folder is opened.
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if matched, matchErr := filepath.Match(bundle, entry.Name()); matchErr == nil && matched &&
			realDirectory(filepath.Join(dir, entry.Name())) {
			return true
		}
	}
	return false
}

// realDirectory is an Lstat: a symbolic link reports its own mode, so a link
// to a directory is not one.
func realDirectory(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.IsDir()
}

// InstallableTools returns the tool rows that can be reported as installed,
// in table order, so a screen can name them without naming an application.
func InstallableTools() []Tool {
	var tools []Tool
	for _, tool := range Tools {
		if len(tool.AppBundles) > 0 {
			tool.AppNames = append([]string(nil), tool.AppNames...)
			tool.AppBundles = append([]string(nil), tool.AppBundles...)
			tool.TemplateFolders = append([]string(nil), tool.TemplateFolders...)
			tools = append(tools, tool)
		}
	}
	return tools
}

// TemplatesToolForIntegration returns the application whose templates the
// reviewed integration with this key can list: the installable tool row that
// declares template folders and whose project format that integration
// supports. Everything comes from the one host table; nothing here names an
// application.
func TemplatesToolForIntegration(integrationKey string) (Tool, bool) {
	if integrationKey == "" {
		return Tool{}, false
	}
	for _, tool := range InstallableTools() {
		if len(tool.TemplateFolders) == 0 {
			continue
		}
		if key, ok := IntegrationKeyForProjectFormat(ProjectFormatForTool(tool.ToolID)); ok && key == integrationKey {
			return tool, true
		}
	}
	return Tool{}, false
}

// ProjectFormatForTool returns the catalog project format whose files a tool
// row is matched by ("" when it has none), so a caller can count a Home's
// library entries per application without naming a format.
func ProjectFormatForTool(toolID string) string {
	tool, ok := ToolByID(toolID)
	if !ok || tool.Match != ToolByExtension || tool.Value == "" {
		return ""
	}
	for _, marker := range Markers {
		if marker.ProjectFormat != "" && marker.Kind == MarkerGlob && strings.EqualFold(marker.Name, "*"+tool.Value) {
			return marker.ProjectFormat
		}
	}
	return ""
}
