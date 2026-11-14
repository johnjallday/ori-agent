package knownfolder

import (
	"path/filepath"
	"strings"
)

// linuxUserFolder interprets the data-only freedesktop user-dirs format. Never
// source it or expand shell expressions. Keeping parsing OS-independent lets
// the macOS unit gate exercise real translated/remapped/disabled settings.
func linuxUserFolder(home, id, content string) (string, error) {
	key := map[string]string{"desktop": "XDG_DESKTOP_DIR", "documents": "XDG_DOCUMENTS_DIR", "downloads": "XDG_DOWNLOAD_DIR"}[id]
	if key == "" {
		return "", ErrUnavailable
	}
	for line := range strings.SplitSeq(content, "\n") {
		left, value, found := strings.Cut(strings.TrimSpace(line), "=")
		if !found || strings.TrimSpace(left) != key {
			continue
		}
		value = strings.TrimSpace(value)
		if !strings.HasPrefix(value, `"`) {
			return "", ErrUnavailable
		}
		var path strings.Builder
		escaped, closed := false, false
		for i := 1; i < len(value); i++ {
			char := value[i]
			if escaped {
				if char != '\\' && char != '"' && char != '$' && char != '`' {
					path.WriteByte('\\')
				}
				path.WriteByte(char)
				escaped = false
				continue
			}
			if char == '\\' {
				escaped = true
				continue
			}
			if char == '"' {
				rest := strings.TrimSpace(value[i+1:])
				if rest != "" && !strings.HasPrefix(rest, "#") {
					return "", ErrUnavailable
				}
				closed = true
				break
			}
			path.WriteByte(char)
		}
		if !closed || escaped {
			return "", ErrUnavailable
		}
		location := path.String()
		switch {
		case location == "$HOME" || location == "${HOME}":
			location = home
		case strings.HasPrefix(location, "$HOME/"):
			location = filepath.Join(home, strings.TrimPrefix(location, "$HOME/"))
		case strings.HasPrefix(location, "${HOME}/"):
			location = filepath.Join(home, strings.TrimPrefix(location, "${HOME}/"))
		}
		if !filepath.IsAbs(location) || strings.ContainsAny(location, "$`\x00\r\n") {
			return "", ErrUnavailable
		}
		return filepath.Clean(location), nil
	}
	return englishPath(home, id)
}
