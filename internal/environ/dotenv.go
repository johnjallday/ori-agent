package environ

import (
	"bufio"
	"os"
	"strings"
)

// LoadDotEnv reads a .env file (if present) from the given directory and
// applies KEY=VALUE lines to the process environment via os.Setenv, skipping
// any key that is already set so real environment variables always take
// precedence over the file. This lets `go run ./cmd/server` pick up local
// secrets (e.g. ORI_GOOGLE_CONNECTION_CLIENT_ID/_SECRET) without requiring
// them to be exported in the shell every session. Missing files are silently
// ignored — .env is optional.
func LoadDotEnv(dir string) {
	path := dir + "/.env"
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if len(value) >= 2 && (value[0] == '"' && value[len(value)-1] == '"' || value[0] == '\'' && value[len(value)-1] == '\'') {
			value = value[1 : len(value)-1]
		}
		if key == "" {
			continue
		}
		if _, exists := os.LookupEnv(key); exists {
			continue
		}
		_ = os.Setenv(key, value)
	}
}
