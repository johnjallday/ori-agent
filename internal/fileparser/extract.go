package fileparser

import (
	"bytes"
	"errors"
	"path/filepath"
	"strings"
)

// Reasons a file's bytes cannot become text. Callers word their own messages;
// neither error carries a filesystem path.
var (
	// ErrNotText is a file that is neither a supported document nor plain text.
	ErrNotText = errors.New("file is not a supported document or plain text")
	// ErrParseFailed is a supported document whose content could not be parsed.
	ErrParseFailed = errors.New("file could not be parsed")
)

// TextSniffBytes is how much of an unknown file is inspected for a NUL byte
// before it is treated as plain text.
const TextSniffBytes = 8 * 1024

// Kinds of text ExtractText returns.
const (
	KindParsed = "parsed"
	KindText   = "text"
)

// LooksLikeText reports whether the first TextSniffBytes of data carry no NUL
// byte.
func LooksLikeText(data []byte) bool {
	head := data
	if len(head) > TextSniffBytes {
		head = head[:TextSniffBytes]
	}
	return bytes.IndexByte(head, 0) == -1
}

// ExtractText turns one file's bytes into text: ParseFile for the extensions it
// supports, plain text for anything else whose start holds no NUL byte, and
// ErrNotText otherwise. It reads only data. It never opens name, follows a path
// written inside the file, decodes audio or runs anything.
func ExtractText(name string, data []byte) (text, kind string, err error) {
	ext := strings.ToLower(filepath.Ext(name))
	if ext == ".epub" {
		return "", "", ErrNotText
	}
	if SupportsExtension(ext) {
		parsed, err := ParseFile(name, data)
		if err != nil {
			return "", "", ErrParseFailed
		}
		return parsed, KindParsed, nil
	}
	if !LooksLikeText(data) {
		return "", "", ErrNotText
	}
	return string(data), KindText, nil
}
