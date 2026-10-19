package backuprecoveryhttp

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/johnjallday/ori-agent/internal/backuprecovery"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
)

const maxRequestBytes = 16 << 10

func parseBody(w http.ResponseWriter, r *http.Request, target any) bool {
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" || (r.Header.Get("Content-Encoding") != "" && r.Header.Get("Content-Encoding") != "identity") {
		_ = orihttp.RespondJSON(w, http.StatusUnsupportedMediaType, orihttp.ErrorResponse{Success: false, Code: "json_required", Error: "Use an uncompressed JSON request."})
		return false
	}
	if r.Body == nil {
		respondError(w, backuprecovery.ErrInvalidRequest)
		return false
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxRequestBytes))
	defer clear(body)
	if err != nil {
		var limit *http.MaxBytesError
		if errors.As(err, &limit) {
			_ = orihttp.RespondJSON(w, http.StatusRequestEntityTooLarge, orihttp.ErrorResponse{Success: false, Code: "request_too_large", Error: "The request exceeds 16 KiB."})
		} else {
			respondError(w, backuprecovery.ErrInvalidRequest)
		}
		return false
	}
	trimmed := bytes.TrimSpace(body)
	if !utf8.Valid(body) || len(trimmed) == 0 || trimmed[0] != '{' {
		respondError(w, backuprecovery.ErrInvalidRequest)
		return false
	}
	// The standard decoder folds field-name case and accepts duplicate keys.
	// Reject those ambiguities before binding any consent/reference DTO.
	scanner := json.NewDecoder(bytes.NewReader(body))
	remaining := 4096
	if !scanValue(scanner, 0, &remaining) {
		respondError(w, backuprecovery.ErrInvalidRequest)
		return false
	}
	if _, err := scanner.Token(); err != io.EOF {
		respondError(w, backuprecovery.ErrInvalidRequest)
		return false
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if decoder.Decode(target) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		respondError(w, backuprecovery.ErrInvalidRequest)
		return false
	}
	return true
}
func scanValue(decoder *json.Decoder, depth int, remaining *int) bool {
	if depth > 8 || *remaining <= 0 {
		return false
	}
	*remaining--
	token, err := decoder.Token()
	if err != nil {
		return false
	}
	switch value := token.(type) {
	case string:
		return !strings.ContainsRune(value, utf8.RuneError)
	case json.Delim:
		switch value {
		case '{':
			seen := map[string]bool{}
			for decoder.More() {
				key, err := decoder.Token()
				name, ok := key.(string)
				if err != nil || !ok || name == "" || seen[name] || name != strings.ToLower(name) || strings.ContainsRune(name, utf8.RuneError) {
					return false
				}
				seen[name] = true
				if !scanValue(decoder, depth+1, remaining) {
					return false
				}
			}
			end, err := decoder.Token()
			return err == nil && end == json.Delim('}')
		case '[':
			for decoder.More() {
				if !scanValue(decoder, depth+1, remaining) {
					return false
				}
			}
			end, err := decoder.Token()
			return err == nil && end == json.Delim(']')
		default:
			return false
		}
	default:
		return true
	}
}
