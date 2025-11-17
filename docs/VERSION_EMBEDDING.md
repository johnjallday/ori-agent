# Version Embedding in Ori Agent

## Problem

When installing ori-agent v0.0.9 from GitHub releases, the version displayed in `http://localhost:8765/settings` showed "dev" instead of "0.0.9".

## Root Cause

The GitHub Actions release workflow was building binaries without embedding version information:

```bash
# Old (incorrect) - no version embedded
GOOS=linux GOARCH=amd64 go build -o bin/ori-agent-linux-amd64 ./cmd/server
```

This caused:
1. The `Version` variable in `internal/version/version.go` remained as "dev"
2. The binary tried to read the `VERSION` file at runtime
3. Since the `VERSION` file wasn't included in releases, it defaulted to "dev"

## Solution

### 1. Build with ldflags

All builds now embed version information using Go's `-ldflags`:

```bash
# New (correct) - version embedded at build time
VERSION="${GITHUB_REF_NAME#v}"  # Extract version from tag (e.g., v0.1.0 → 0.1.0)
GIT_COMMIT="${GITHUB_SHA:0:7}"  # Short git commit hash
BUILD_DATE=$(date -u +"%Y-%m-%dT%H:%M:%SZ")  # UTC timestamp

LDFLAGS="-X 'github.com/johnjallday/ori-agent/internal/version.Version=$VERSION' \
         -X 'github.com/johnjallday/ori-agent/internal/version.GitCommit=$GIT_COMMIT' \
         -X 'github.com/johnjallday/ori-agent/internal/version.BuildDate=$BUILD_DATE'"

go build -ldflags "$LDFLAGS" -o bin/ori-agent ./cmd/server
```

### 2. Updated Build Systems

**Makefile** (local development):
- Reads version from `VERSION` file
- Gets git commit from `git rev-parse`
- Generates current timestamp
- Builds with ldflags automatically

```bash
make build  # Version automatically embedded
```

**GitHub Actions** (releases):
- Extracts version from git tag
- Uses GitHub SHA for commit hash
- Generates UTC timestamp
- Applies to both server binaries AND macOS menubar app

**Build Scripts** (`scripts/build-release.sh`):
- Already had ldflags support
- No changes needed

## How It Works

### Version Resolution Priority

The `internal/version/version.go` file uses this priority:

1. **Build-time ldflags** (highest priority)
   - If `Version` variable was set via `-ldflags`, use it
   - This is the preferred method

2. **VERSION file** (fallback)
   - If version is "dev", try reading `VERSION` file in:
     - Current working directory
     - Directory relative to executable

3. **Default** (last resort)
   - If all else fails, return "dev"

### Code Structure

```go
// internal/version/version.go
var (
    Version   = "dev"     // Set via -ldflags at build time
    GitCommit = "unknown" // Set via -ldflags at build time
    BuildDate = "unknown" // Set via -ldflags at build time
)

func GetVersion() string {
    // 1. Check if version was embedded at build time
    if Version != "dev" && Version != "" {
        return Version  // ✅ This path is now always used in releases
    }

    // 2. Try reading VERSION file (fallback for dev builds)
    if data, err := os.ReadFile("VERSION"); err == nil {
        return strings.TrimSpace(string(data))
    }

    // 3. Default
    return "dev"
}
```

## Benefits

1. **Self-contained binaries**: No need to distribute VERSION file with releases
2. **Accurate versioning**: Version always matches the release tag
3. **Additional metadata**: Git commit and build date available
4. **Consistent behavior**: Same approach for all builds (local, CI, releases)

## Verification

Check version information in a binary:

```bash
# Method 1: Using strings command
strings ./bin/ori-agent | grep -E "^[0-9]+\.[0-9]+\.[0-9]+"

# Method 2: Run the server and check logs
./bin/ori-agent
# Look for version in startup logs

# Method 3: Check settings page
# Visit http://localhost:8765/settings after starting the server
```

## Local Development

When building locally:

```bash
# Using Makefile (recommended)
make build
# Output: Version: 0.1.0 | Commit: f73710d | Date: 2025-11-17T13:48:17Z

# Manual build with version
VERSION=$(cat VERSION)
go build -ldflags "-X 'github.com/johnjallday/ori-agent/internal/version.Version=$VERSION'" \
    -o bin/ori-agent ./cmd/server

# Quick dev build (will show "dev" version)
go build -o bin/ori-agent ./cmd/server
```

## Next Release

For the next release (v0.1.0), the version will be correctly embedded:

1. Update `VERSION` file to `0.1.0`
2. Commit changes
3. Create and push tag: `git tag -a v0.1.0 -m "Release v0.1.0"`
4. GitHub Actions builds binaries with version `0.1.0` embedded
5. Users download and see "Version 0.1.0" in settings

## Migration Notes

**For existing v0.0.9 users:**
- If you installed v0.0.9 from GitHub releases, you may see "dev" version
- This will be fixed in v0.1.0 and all future releases
- No action needed - just update to v0.1.0 when available

**For developers:**
- Always use `make build` for local builds to get proper versioning
- The `VERSION` file is still used for local development
- CI/CD will use git tags for version information
