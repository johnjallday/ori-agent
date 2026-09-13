package plugin

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"
)

// resetHarness builds a disposable installation with a personal skills root
// outside it. Everything is domain neutral: no shipped plugin, marketplace,
// account, or real user location is involved.
type resetHarness struct {
	t     *testing.T
	root  string
	data  string
	home  string
	paths ResetPaths
}

func newResetHarness(t *testing.T) *resetHarness {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolve harness root: %v", err)
	}
	h := &resetHarness{
		t: t, root: root,
		data: filepath.Join(root, "data"),
		home: filepath.Join(root, "home"),
	}
	h.paths = DefaultResetPaths(h.data, filepath.Join(h.home, ".agents", "skills"))
	for _, dir := range []string{
		h.paths.PluginsDir, h.paths.CloneDir, h.paths.SkillsRoot,
		filepath.Join(h.paths.PluginsDir, "state"),
		filepath.Join(h.paths.PluginsDir, "artifacts"),
		filepath.Join(h.paths.PluginsDir, "preview"),
		filepath.Join(root, "external"),
	} {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			t.Fatalf("create %s: %v", dir, err)
		}
	}
	return h
}

func (h *resetHarness) write(path string, content string) {
	h.t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		h.t.Fatalf("create parent of %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		h.t.Fatalf("write %s: %v", path, err)
	}
}

func (h *resetHarness) writeRegistry(records ...InstalledPlugin) {
	h.t.Helper()
	data, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		h.t.Fatalf("encode registry: %v", err)
	}
	h.write(h.paths.RegistryPath(), string(data))
}

func (h *resetHarness) writeRawRegistry(content string) {
	h.t.Helper()
	h.write(h.paths.RegistryPath(), content)
}

func (h *resetHarness) writeMCPRegistry(names ...string) {
	h.t.Helper()
	document := struct {
		Servers []map[string]any `json:"servers"`
	}{}
	for _, name := range names {
		document.Servers = append(document.Servers, map[string]any{
			"name": name, "command": "unused-in-offline-reset", "transport": "stdio", "enabled": false,
		})
	}
	data, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		h.t.Fatalf("encode mcp registry: %v", err)
	}
	h.write(h.paths.MCPRegistry, string(data))
}

// seedManaged creates one fully-loaded managed plugin: a managed clone, a copied
// personal skill, namespaced surface state in both managed locations, a managed
// artifact, and a namespaced MCP registration.
func (h *resetHarness) seedManaged(name string) InstalledPlugin {
	h.t.Helper()
	clone := filepath.Join(h.paths.CloneDir, name+"-repo")
	h.write(filepath.Join(clone, ".claude-plugin", "plugin.json"), `{"name":"`+name+`"}`)
	h.write(filepath.Join(h.paths.SkillsRoot, name+"-skill", "SKILL.md"), "# harness skill\n")
	digest := sha256.Sum256([]byte(name))
	h.write(filepath.Join(h.paths.PluginsDir, "state", hex.EncodeToString(digest[:]), "workspace.json"), `{"schema_version":1}`)
	h.write(filepath.Join(h.paths.PluginsDir, "state", name, "service-owned.txt"), "plugin service data\n")
	h.write(filepath.Join(h.paths.PluginsDir, "artifacts", name, "abcdef0123456789", "svc", "bin"), "artifact\n")
	return InstalledPlugin{
		Name: name, Version: "1.2.3", Source: "https://example.test/" + name + ".git",
		Format: FormatClaude, InstallDir: clone,
		MCPServers:        []string{NamespacedServerName(name, "tools")},
		Skills:            []string{name + "-skill"},
		WorkspaceSurfaces: &SurfaceContribution{},
		ResolvedArtifacts: []ResolvedArtifact{{ServiceID: "svc", Available: true}},
		Generation:        2, Enabled: true, InstalledAt: time.Unix(0, 0).UTC(),
	}
}

// seedLinked creates a plugin installed from an external directory Ori did not
// create. Its bytes must survive reset.
func (h *resetHarness) seedLinked(name string) InstalledPlugin {
	h.t.Helper()
	source := filepath.Join(h.root, "external", name)
	h.write(filepath.Join(source, ".claude-plugin", "plugin.json"), `{"name":"`+name+`"}`)
	h.write(filepath.Join(h.paths.SkillsRoot, name+"-skill", "SKILL.md"), "# linked harness skill\n")
	return InstalledPlugin{
		Name: name, Version: "0.1.0", Source: source, Format: FormatClaude, InstallDir: source,
		Skills: []string{name + "-skill"}, Generation: 1, Enabled: false, InstalledAt: time.Unix(0, 0).UTC(),
	}
}

// snapshot hashes every path under the harness root so a test can prove that an
// operation changed nothing at all.
func (h *resetHarness) snapshot() map[string]string {
	h.t.Helper()
	result := make(map[string]string)
	err := filepath.WalkDir(h.root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(h.root, path)
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			result[relative] = info.Mode().String()
			return nil
		}
		data, err := os.ReadFile(path) // #nosec G304 -- harness-owned temporary tree
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		result[relative] = info.Mode().String() + ":" + hex.EncodeToString(sum[:])
		return nil
	})
	if err != nil {
		h.t.Fatalf("snapshot harness: %v", err)
	}
	return result
}

func (h *resetHarness) assertUnchanged(before map[string]string) {
	h.t.Helper()
	after := h.snapshot()
	for path, want := range before {
		got, ok := after[path]
		if !ok {
			h.t.Errorf("inspection removed %s", path)
			continue
		}
		if got != want {
			h.t.Errorf("inspection changed %s", path)
		}
	}
	for path := range after {
		if _, ok := before[path]; !ok {
			h.t.Errorf("inspection created %s", path)
		}
	}
}

func (h *resetHarness) exists(path string) bool {
	h.t.Helper()
	_, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false
	}
	if err != nil {
		h.t.Fatalf("stat %s: %v", path, err)
	}
	return true
}

func problemCodes(problems []ResetProblem) []string {
	codes := make([]string, 0, len(problems))
	for _, problem := range problems {
		codes = append(codes, problem.Code)
	}
	sort.Strings(codes)
	return codes
}

func TestInspectResetDescribesEveryOwnedComponent(t *testing.T) {
	h := newResetHarness(t)
	managed := h.seedManaged("harness-alpha")
	h.writeRegistry(managed)
	h.writeMCPRegistry(NamespacedServerName("harness-alpha", "tools"), "user-owned-server")

	before := h.snapshot()
	inventory, problems := InspectReset(h.paths)
	if len(problems) != 0 {
		t.Fatalf("unexpected problems: %v", problemCodes(problems))
	}
	h.assertUnchanged(before)

	if len(inventory.Items) != 1 {
		t.Fatalf("expected one reviewed plugin, got %d", len(inventory.Items))
	}
	item := inventory.Items[0]
	if item.Name != "harness-alpha" || item.Version != "1.2.3" || !item.Enabled || item.Generation != 2 {
		t.Fatalf("identity not reported exactly: %+v", item)
	}
	if !item.Surfaces || item.Artifacts != 1 {
		t.Fatalf("component summary not reported: %+v", item)
	}
	if !slices.Equal(item.MCPServers, []string{NamespacedServerName("harness-alpha", "tools")}) {
		t.Fatalf("mcp registrations not reported exactly: %v", item.MCPServers)
	}
	if !slices.Equal(item.Skills, []string{"harness-alpha-skill"}) {
		t.Fatalf("skills not reported exactly: %v", item.Skills)
	}
	if !item.Managed || item.SourceDisposition() != "managed clone (removed)" {
		t.Fatalf("managed clone not recognised: %+v", item)
	}
	if inventory.RegistryDigest == "" || inventory.SkillsRoot != h.paths.SkillsRoot {
		t.Fatalf("inventory evidence incomplete: %+v", inventory)
	}
}

func TestInspectResetSeparatesManagedFromLinkedSources(t *testing.T) {
	h := newResetHarness(t)
	managed := h.seedManaged("harness-alpha")
	linked := h.seedLinked("harness-beta")
	h.writeRegistry(managed, linked)

	inventory, problems := InspectReset(h.paths)
	if len(problems) != 0 {
		t.Fatalf("unexpected problems: %v", problemCodes(problems))
	}
	if len(inventory.Items) != 2 {
		t.Fatalf("expected two reviewed plugins, got %d", len(inventory.Items))
	}
	if inventory.Items[0].Name != "harness-alpha" || inventory.Items[1].Name != "harness-beta" {
		t.Fatalf("inventory is not deterministically ordered: %+v", inventory.Items)
	}
	if !inventory.Items[0].Managed {
		t.Fatal("managed clone was not recognised as managed")
	}
	if inventory.Items[1].Managed || inventory.Items[1].SourceDisposition() != "linked source (kept)" {
		t.Fatalf("linked source was treated as managed: %+v", inventory.Items[1])
	}
}

func TestInspectResetEmptyInventoryIsAVerifiedNoOp(t *testing.T) {
	h := newResetHarness(t)
	inventory, problems := InspectReset(h.paths)
	if len(problems) != 0 {
		t.Fatalf("absent registry must not block: %v", problemCodes(problems))
	}
	if len(inventory.Items) != 0 {
		t.Fatalf("expected no reviewed plugins, got %d", len(inventory.Items))
	}
	empty, err := ResetRegistryEmpty(h.paths)
	if err != nil || !empty {
		t.Fatalf("absent registry should read as empty: %v %v", empty, err)
	}
}

func TestInspectResetBlocksUnsafeAndAmbiguousOwnership(t *testing.T) {
	cases := []struct {
		name   string
		record func(*resetHarness) []InstalledPlugin
		want   string
	}{
		{
			name: "traversing plugin name",
			record: func(h *resetHarness) []InstalledPlugin {
				return []InstalledPlugin{{Name: "../escape", InstallDir: h.paths.CloneDir}}
			},
			want: ResetProblemNameUnsafe,
		},
		{
			name: "absolute plugin name",
			record: func(h *resetHarness) []InstalledPlugin {
				return []InstalledPlugin{{Name: "/etc", InstallDir: h.paths.CloneDir}}
			},
			want: ResetProblemNameUnsafe,
		},
		{
			name: "hidden plugin name",
			record: func(h *resetHarness) []InstalledPlugin {
				return []InstalledPlugin{{Name: ".staging-abc", InstallDir: h.paths.CloneDir}}
			},
			want: ResetProblemNameUnsafe,
		},
		{
			name: "duplicate records",
			record: func(h *resetHarness) []InstalledPlugin {
				first := h.seedManaged("harness-alpha")
				return []InstalledPlugin{first, first}
			},
			want: ResetProblemDuplicateRecord,
		},
		{
			name: "skill escaping the personal skills root",
			record: func(h *resetHarness) []InstalledPlugin {
				record := h.seedManaged("harness-alpha")
				record.Skills = []string{"../../escape"}
				return []InstalledPlugin{record}
			},
			want: ResetProblemComponentUnsafe,
		},
		{
			name: "unnamespaced mcp registration",
			record: func(h *resetHarness) []InstalledPlugin {
				record := h.seedManaged("harness-alpha")
				record.MCPServers = []string{"user-owned-server"}
				return []InstalledPlugin{record}
			},
			want: ResetProblemComponentUnsafe,
		},
		{
			name: "two plugins claiming one personal skill",
			record: func(h *resetHarness) []InstalledPlugin {
				first := h.seedManaged("harness-alpha")
				second := h.seedLinked("harness-beta")
				second.Skills = first.Skills
				return []InstalledPlugin{first, second}
			},
			want: ResetProblemSkillAmbiguous,
		},
		{
			name: "unresolvable relative install root",
			record: func(h *resetHarness) []InstalledPlugin {
				record := h.seedManaged("harness-alpha")
				record.InstallDir = filepath.Join("relative", "never-created")
				record.Source = "https://example.test/harness-alpha.git"
				return []InstalledPlugin{record}
			},
			want: ResetProblemRootUnresolved,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			h := newResetHarness(t)
			h.writeRegistry(testCase.record(h)...)
			before := h.snapshot()
			inventory, problems := InspectReset(h.paths)
			h.assertUnchanged(before)
			if !slices.Contains(problemCodes(problems), testCase.want) {
				t.Fatalf("expected blocker %q, got %v", testCase.want, problemCodes(problems))
			}
			if len(inventory.Items) != 0 {
				t.Fatalf("a blocked inspection must not report a partial inventory: %+v", inventory.Items)
			}
		})
	}
}

func TestInspectResetBlocksUnexpectedSkillDestinations(t *testing.T) {
	for _, kind := range []string{"symlink", "regular file"} {
		t.Run(kind, func(t *testing.T) {
			h := newResetHarness(t)
			record := h.seedManaged("harness-alpha")
			destination := filepath.Join(h.paths.SkillsRoot, "harness-alpha-skill")
			if err := os.RemoveAll(destination); err != nil {
				t.Fatalf("clear destination: %v", err)
			}
			if kind == "symlink" {
				target := filepath.Join(h.root, "external", "linked-skill")
				if err := os.MkdirAll(target, 0o750); err != nil {
					t.Fatalf("create link target: %v", err)
				}
				if err := os.Symlink(target, destination); err != nil {
					t.Skipf("symlinks unavailable: %v", err)
				}
			} else {
				h.write(destination, "not a skill directory\n")
			}
			h.writeRegistry(record)
			before := h.snapshot()
			_, problems := InspectReset(h.paths)
			h.assertUnchanged(before)
			if !slices.Contains(problemCodes(problems), ResetProblemSkillUnexpected) {
				t.Fatalf("expected an unexpected-destination blocker, got %v", problemCodes(problems))
			}
		})
	}
}

func TestInspectResetBlocksUnreadableRegistries(t *testing.T) {
	t.Run("corrupt json", func(t *testing.T) {
		h := newResetHarness(t)
		h.writeRawRegistry("{not json")
		_, problems := InspectReset(h.paths)
		if !slices.Contains(problemCodes(problems), ResetProblemRegistryUnreadable) {
			t.Fatalf("expected an unreadable-registry blocker, got %v", problemCodes(problems))
		}
	})
	t.Run("directory in place of the registry", func(t *testing.T) {
		h := newResetHarness(t)
		if err := os.MkdirAll(h.paths.RegistryPath(), 0o750); err != nil {
			t.Fatalf("create directory: %v", err)
		}
		_, problems := InspectReset(h.paths)
		if !slices.Contains(problemCodes(problems), ResetProblemRegistryUnreadable) {
			t.Fatalf("expected an unreadable-registry blocker, got %v", problemCodes(problems))
		}
	})
	t.Run("more plugins than one review supports", func(t *testing.T) {
		h := newResetHarness(t)
		records := make([]InstalledPlugin, 0, MaxResetItems+1)
		for index := 0; index <= MaxResetItems; index++ {
			records = append(records, InstalledPlugin{
				Name: "harness-" + strings.Repeat("x", index%8) + itoa(index), InstallDir: h.paths.CloneDir,
			})
		}
		h.writeRegistry(records...)
		_, problems := InspectReset(h.paths)
		if !slices.Contains(problemCodes(problems), ResetProblemInventoryTooLarge) {
			t.Fatalf("expected an inventory-size blocker, got %v", problemCodes(problems))
		}
	})
}

func TestInspectResetBlocksUnresolvedOwnerRoots(t *testing.T) {
	_, problems := InspectReset(ResetPaths{PluginsDir: "relative/plugins"})
	if !slices.Contains(problemCodes(problems), ResetProblemOwnerUnavailable) {
		t.Fatalf("expected an owner-unavailable blocker, got %v", problemCodes(problems))
	}
}

func TestRemoveResetItemRemovesExactlyItsOwnComponents(t *testing.T) {
	h := newResetHarness(t)
	managed := h.seedManaged("harness-alpha")
	linked := h.seedLinked("harness-beta")
	h.writeRegistry(managed, linked)
	h.writeMCPRegistry(NamespacedServerName("harness-alpha", "tools"), "user-owned-server")
	h.write(filepath.Join(h.paths.SkillsRoot, "user-authored-skill", "SKILL.md"), "# personal\n")
	h.write(filepath.Join(h.paths.PluginsDir, "marketplaces.json"), `[{"name":"harness-market"}]`)

	inventory, problems := InspectReset(h.paths)
	if len(problems) != 0 {
		t.Fatalf("unexpected problems: %v", problemCodes(problems))
	}
	target := inventory.Items[0]
	if err := RemoveResetItem(h.paths, target); err != nil {
		t.Fatalf("remove reviewed plugin: %v", err)
	}

	digest := sha256.Sum256([]byte("harness-alpha"))
	for _, gone := range []string{
		filepath.Join(h.paths.CloneDir, "harness-alpha-repo"),
		filepath.Join(h.paths.SkillsRoot, "harness-alpha-skill"),
		filepath.Join(h.paths.PluginsDir, "state", hex.EncodeToString(digest[:])),
		filepath.Join(h.paths.PluginsDir, "state", "harness-alpha"),
		filepath.Join(h.paths.PluginsDir, "artifacts", "harness-alpha"),
	} {
		if h.exists(gone) {
			t.Errorf("owned component survived removal: %s", gone)
		}
	}
	for _, kept := range []string{
		filepath.Join(h.root, "external", "harness-beta"),
		filepath.Join(h.paths.SkillsRoot, "harness-beta-skill"),
		filepath.Join(h.paths.SkillsRoot, "user-authored-skill"),
		filepath.Join(h.paths.PluginsDir, "marketplaces.json"),
		h.paths.SkillsRoot,
	} {
		if !h.exists(kept) {
			t.Errorf("unrelated or linked location was removed: %s", kept)
		}
	}

	names, err := mcpNames(h.paths.MCPRegistry)
	if err != nil {
		t.Fatalf("read mcp registry: %v", err)
	}
	if !slices.Equal(names, []string{"user-owned-server"}) {
		t.Fatalf("unrelated MCP registrations were not preserved exactly: %v", names)
	}

	remaining, _, err := readResetRegistry(h.paths.RegistryPath())
	if err != nil {
		t.Fatalf("read registry: %v", err)
	}
	if len(remaining) != 1 || remaining[0].Name != "harness-beta" {
		t.Fatalf("registry did not retain the unreviewed record: %+v", remaining)
	}
}

func TestRemoveResetItemIsIdempotent(t *testing.T) {
	h := newResetHarness(t)
	h.writeRegistry(h.seedManaged("harness-alpha"))
	h.writeMCPRegistry(NamespacedServerName("harness-alpha", "tools"))
	inventory, problems := InspectReset(h.paths)
	if len(problems) != 0 {
		t.Fatalf("unexpected problems: %v", problemCodes(problems))
	}
	item := inventory.Items[0]
	for attempt := range 3 {
		if err := RemoveResetItem(h.paths, item); err != nil {
			t.Fatalf("attempt %d: %v", attempt, err)
		}
		removed, err := ResetItemRemoved(h.paths, item)
		if err != nil || !removed {
			t.Fatalf("attempt %d did not verify removal: %v %v", attempt, removed, err)
		}
	}
}

func TestResetItemRemovedRejectsRegistryAbsenceAlone(t *testing.T) {
	h := newResetHarness(t)
	record := h.seedManaged("harness-alpha")
	h.writeRegistry(record)
	inventory, problems := InspectReset(h.paths)
	if len(problems) != 0 {
		t.Fatalf("unexpected problems: %v", problemCodes(problems))
	}
	item := inventory.Items[0]
	// Drop only the registry row, exactly as a hand-edited or partially applied
	// installation would. The external skill copy still exists.
	if err := deleteResetRecord(h.paths.RegistryPath(), item.Name); err != nil {
		t.Fatalf("delete record: %v", err)
	}
	removed, err := ResetItemRemoved(h.paths, item)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if removed {
		t.Fatal("a missing registry row was accepted as proof that external components were removed")
	}
}

func TestRemoveResetPreviewCacheKeepsOwnedRoot(t *testing.T) {
	h := newResetHarness(t)
	h.write(filepath.Join(h.paths.PreviewRoot(), "harness-alpha-repo", "plugin.json"), "{}")
	if err := RemoveResetPreviewCache(h.paths); err != nil {
		t.Fatalf("remove preview cache: %v", err)
	}
	if h.exists(h.paths.PreviewRoot()) {
		t.Fatal("preview cache was not removed")
	}
	if !h.exists(h.paths.PluginsDir) {
		t.Fatal("preview removal escaped to the managed plugins root")
	}
	if err := RemoveResetPreviewCache(h.paths); err != nil {
		t.Fatalf("preview cache removal is not idempotent: %v", err)
	}
}

func TestRemoveResetItemRefusesUnsafeEvidence(t *testing.T) {
	h := newResetHarness(t)
	h.writeRegistry(h.seedManaged("harness-alpha"))
	before := h.snapshot()
	if err := RemoveResetItem(h.paths, ResetItem{Name: "../escape"}); err == nil {
		t.Fatal("an unsafe plugin name was accepted")
	}
	if err := RemoveResetItem(h.paths, ResetItem{Name: "harness-alpha", Skills: []string{"../escape"}}); err == nil {
		t.Fatal("an unsafe skill name was accepted")
	}
	if err := RemoveResetItem(h.paths, ResetItem{
		Name: "harness-alpha", Managed: true, InstallRoot: filepath.Join(h.root, "external", "elsewhere"),
	}); err == nil {
		t.Fatal("a managed clone outside the managed root was accepted")
	}
	h.assertUnchanged(before)
}

func mcpNames(path string) ([]string, error) {
	data, err := os.ReadFile(path) // #nosec G304 -- harness-owned temporary tree
	if err != nil {
		return nil, err
	}
	var document struct {
		Servers []struct {
			Name string `json:"name"`
		} `json:"servers"`
	}
	if err := json.Unmarshal(data, &document); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(document.Servers))
	for _, server := range document.Servers {
		names = append(names, server.Name)
	}
	sort.Strings(names)
	return names, nil
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	digits := ""
	for value > 0 {
		digits = string(rune('0'+value%10)) + digits
		value /= 10
	}
	return digits
}
