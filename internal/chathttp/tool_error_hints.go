package chathttp

import (
	"encoding/json"
	"fmt"
	"strings"
)

type pluginManagerToolArgs struct {
	Operation string `json:"operation"`
	Plugin    string `json:"plugin"`
	Version   string `json:"version"`
}

func augmentToolExecutionError(toolName, args string, err error) string {
	base := fmt.Sprintf("❌ Error executing %s: %v", toolName, err)
	if err == nil {
		return base
	}

	// Add copy/paste hints for common plugin-manager release failures.
	if toolName != "ori_plugin_manager" {
		return base
	}

	errLower := strings.ToLower(err.Error())
	if !strings.Contains(errLower, "uncommitted changes") && !strings.Contains(errLower, "dirty") {
		return base
	}

	var parsed pluginManagerToolArgs
	_ = json.Unmarshal([]byte(args), &parsed)

	plugin := parsed.Plugin
	version := parsed.Version

	pluginRepo := "<path-to-plugin-repo>"
	if plugin != "" {
		pluginRepo = "../plugins/" + plugin
	}

	rerun := "release <plugin> <version>"
	if plugin != "" && version != "" {
		rerun = fmt.Sprintf("release %s %s", plugin, version)
	} else if plugin != "" {
		rerun = fmt.Sprintf("release %s <version>", plugin)
	}

	return base + "\n\n" +
		"Fix (pick one), then rerun:\n\n" +
		"```bash\n" +
		fmt.Sprintf("# Inspect what's dirty\ngit -C %s status --porcelain\n\n", pluginRepo) +
		"# Option A: commit\n" +
		fmt.Sprintf("git -C %s add -A\n", pluginRepo) +
		fmt.Sprintf("git -C %s commit -m \"chore: prep release\"\n\n", pluginRepo) +
		"# Option B: stash\n" +
		fmt.Sprintf("git -C %s stash push -u -m \"ori release\"\n\n", pluginRepo) +
		"# Back in Ori chat:\n" +
		rerun + "\n" +
		"```\n"
}
