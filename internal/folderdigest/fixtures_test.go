package folderdigest

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// materializeTree builds testdata/trees/<name>.txt in a temp dir and returns
// the tree's root. Manifest lines are one entry each:
//
//	dir/                 a directory
//	file.ext @3d         a file last modified three days ago (default today)
//	name-{1..5}.ext      expands to five files
//	link -> ../outside   a symbolic link (target relative to the link)
//	# comment
//
// Git keeps no modification times, so the dates the verdicts depend on are
// set here rather than committed.
func materializeTree(t *testing.T, name string) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest, err := os.ReadFile(filepath.Join("testdata", "trees", name+".txt"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	for lineNo, raw := range strings.Split(string(manifest), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if before, target, ok := strings.Cut(line, " -> "); ok {
			link := filepath.Join(root, strings.TrimSpace(before))
			if err := os.Symlink(strings.TrimSpace(target), link); err != nil {
				t.Fatalf("%s:%d: %v", name, lineNo+1, err)
			}
			continue
		}
		fields := strings.Fields(line)
		age := 0
		if len(fields) > 1 {
			n, err := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(fields[1], "@"), "d"))
			if err != nil {
				t.Fatalf("%s:%d: bad age %q", name, lineNo+1, fields[1])
			}
			age = n
		}
		modTime := now.AddDate(0, 0, -age)
		for _, rel := range expandRange(fields[0]) {
			full := filepath.Join(root, rel)
			if strings.HasSuffix(rel, "/") {
				if err := os.MkdirAll(full, 0o755); err != nil {
					t.Fatal(err)
				}
				continue
			}
			if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(full, []byte("fixture"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.Chtimes(full, modTime, modTime); err != nil {
				t.Fatal(err)
			}
		}
	}
	return root
}

var rangePattern = regexp.MustCompile(`\{(\d+)\.\.(\d+)\}`)

func expandRange(name string) []string {
	m := rangePattern.FindStringSubmatchIndex(name)
	if m == nil {
		return []string{name}
	}
	from, _ := strconv.Atoi(name[m[2]:m[3]])
	to, _ := strconv.Atoi(name[m[4]:m[5]])
	var out []string
	for i := from; i <= to; i++ {
		out = append(out, name[:m[0]]+strconv.Itoa(i)+name[m[1]:])
	}
	return out
}

// makeOverflowTree generates more entries than the cap allows, spread over
// subfolders so no single directory read exhausts it.
func makeOverflowTree(t *testing.T, entries int) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "overflow")
	perDir := 500
	for dir := 0; dir*perDir < entries; dir++ {
		sub := filepath.Join(root, fmt.Sprintf("batch-%02d", dir))
		if err := os.MkdirAll(sub, 0o755); err != nil {
			t.Fatal(err)
		}
		for i := range perDir {
			if err := os.WriteFile(filepath.Join(sub, fmt.Sprintf("f%d.txt", i)), nil, 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	return root
}

// makeUnreadable sets every regular file under root to mode 000 and restores
// the modes when the test ends. Directories stay readable so they can still
// be listed; only file contents become inaccessible.
func makeUnreadable(t *testing.T, root string) {
	t.Helper()
	var restored []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type().IsRegular() {
			if err := os.Chmod(path, 0); err != nil {
				return err
			}
			restored = append(restored, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, path := range restored {
			_ = os.Chmod(path, 0o644)
		}
	})
}

// fixtureNow is the clock the tests decide verdicts with.
func fixtureNow() time.Time { return time.Now() }
