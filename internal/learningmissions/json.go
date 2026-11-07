package learningmissions

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"unicode/utf8"
)

// DecodeStrict is shared by owner requests and model proposals. It rejects
// duplicate keys, case aliases, unknown fields, nulls, nesting and trailing JSON
// before any authority-bearing mutation. max is a host constant, never input.
func DecodeStrict(data []byte, max int, into any) error {
	data = bytes.TrimSpace(data)
	if len(data) == 0 || len(data) > max || data[0] != '{' || !utf8.Valid(data) {
		return ErrInvalid
	}
	probe := json.NewDecoder(bytes.NewReader(data))
	if err := uniqueJSON(probe, 0); err != nil {
		return ErrInvalid
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(into) != nil || decoder.Decode(new(any)) != io.EOF {
		return ErrInvalid
	}
	return nil
}

func uniqueJSON(decoder *json.Decoder, depth int) error {
	if depth > 8 {
		return ErrInvalid
	}
	token, err := decoder.Token()
	if err != nil || token == nil {
		return ErrInvalid
	}
	delim, compound := token.(json.Delim)
	if !compound {
		return nil
	}
	if delim != '{' && delim != '[' {
		return ErrInvalid
	}
	seen := map[string]bool{}
	count := 0
	for decoder.More() {
		count++
		if count > 64 {
			return ErrInvalid
		}
		if delim == '{' {
			key, err := decoder.Token()
			name, ok := key.(string)
			if err != nil || !ok || seen[name] || name != strings.ToLower(name) {
				return ErrInvalid
			}
			seen[name] = true
		}
		if err := uniqueJSON(decoder, depth+1); err != nil {
			return err
		}
	}
	_, err = decoder.Token()
	return err
}
