package workspacecontinuity

import (
	"bytes"
	"encoding/json"
	"io"
	"reflect"
	"strings"
	"unicode/utf8"
)

// readBounded bounds allocation before JSON decoding. Reader errors deliberately
// do not expose source filenames or private content through the format API.
func readBounded(reader io.Reader, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, ErrIncomplete
	}
	if int64(len(data)) > limit {
		return nil, ErrLimit
	}
	return data, nil
}

// DecodeDocument validates a bounded canonical file without filling defaults
// or requiring newer optional fields on a legacy file. Domain owners must still
// validate identities and decide whether missing fields are supported legacy.
func DecodeDocument(data []byte, destination any, limit int) error {
	if limit <= 0 || limit > MaxChunkBytes || len(data) > limit {
		return ErrLimit
	}
	return strictJSON(data, destination)
}

// strictJSON rejects duplicate keys even inside RawMessage bodies. The standard
// JSON decoder otherwise silently accepts the last duplicate and replaces invalid
// UTF-8. Depth/tokens are also bounded to avoid parser recursion/resource abuse.
func strictJSON(data []byte, destination any) error {
	if !utf8.Valid(data) || !validSurrogates(data) {
		return ErrInvalid
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	tokens := 0
	if err := scanJSONValue(decoder, 0, &tokens, reflect.TypeOf(destination)); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return ErrInvalid
	}
	decoder = json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return ErrInvalid
	}
	return nil
}

// encoding/json also replaces lone escaped UTF-16 surrogates. Reject them rather
// than silently changing imported historical text to the replacement character.
func validSurrogates(data []byte) bool {
	quoted := false
	for i := 0; i < len(data); i++ {
		if data[i] == '"' {
			quoted = !quoted
			continue
		}
		if !quoted || data[i] != '\\' {
			continue
		}
		i++
		if i >= len(data) {
			return false
		}
		if data[i] != 'u' {
			continue
		}
		value, ok := hexQuad(data[i+1:])
		if !ok {
			return false
		}
		i += 4
		if value >= 0xdc00 && value <= 0xdfff {
			return false
		}
		if value >= 0xd800 && value <= 0xdbff {
			if i+6 >= len(data) || data[i+1] != '\\' || data[i+2] != 'u' {
				return false
			}
			low, ok := hexQuad(data[i+3:])
			if !ok || low < 0xdc00 || low > 0xdfff {
				return false
			}
			i += 6
		}
	}
	return true
}

func hexQuad(data []byte) (uint16, bool) {
	if len(data) < 4 {
		return 0, false
	}
	var value uint16
	for _, char := range data[:4] {
		var digit byte
		switch {
		case char >= '0' && char <= '9':
			digit = char - '0'
		case char >= 'a' && char <= 'f':
			digit = char - 'a' + 10
		case char >= 'A' && char <= 'F':
			digit = char - 'A' + 10
		default:
			return 0, false
		}
		value = value*16 + uint16(digit)
	}
	return value, true
}

func scanJSONValue(decoder *json.Decoder, depth int, tokens *int, expected reflect.Type) error {
	nullable := expected != nil && expected.Kind() == reflect.Pointer
	expected = jsonValueType(expected)
	if depth > 64 || *tokens > 200000 {
		return ErrLimit
	}
	*tokens++
	token, err := decoder.Token()
	if err != nil {
		return ErrInvalid
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		if token == nil && expected != nil && !nullable {
			switch expected.Kind() {
			case reflect.Bool, reflect.String, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
				reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Float32, reflect.Float64:
				// encoding/json silently treats null scalars as their zero value.
				// That would fabricate a known false/zero/empty source choice.
				return ErrInvalid
			}
		}
		return nil
	}
	switch delimiter {
	case '{':
		keys := map[string]bool{}
		for decoder.More() {
			key, err := decoder.Token()
			name, ok := key.(string)
			if err != nil || !ok || keys[name] {
				return ErrInvalid
			}
			keys[name] = true
			var fieldType reflect.Type
			if expected != nil {
				switch expected.Kind() {
				case reflect.Struct:
					for index := 0; index < expected.NumField(); index++ {
						field := expected.Field(index)
						key := strings.Split(field.Tag.Get("json"), ",")[0]
						if key == "" {
							key = field.Name
						}
						if field.IsExported() && key == name {
							fieldType = field.Type
							break
						}
					}
					// encoding/json accepts case-insensitive tag aliases. Reject
					// those too: version/Version must not set the same field twice.
					if fieldType == nil {
						return ErrInvalid
					}
				case reflect.Map:
					fieldType = expected.Elem()
				}
			}
			if err := scanJSONValue(decoder, depth+1, tokens, fieldType); err != nil {
				return err
			}
		}
		token, err = decoder.Token()
		if err != nil || token != json.Delim('}') {
			return ErrInvalid
		}
	case '[':
		var element reflect.Type
		if expected != nil && (expected.Kind() == reflect.Slice || expected.Kind() == reflect.Array) {
			element = expected.Elem()
		}
		for decoder.More() {
			if err := scanJSONValue(decoder, depth+1, tokens, element); err != nil {
				return err
			}
		}
		token, err = decoder.Token()
		if err != nil || token != json.Delim(']') {
			return ErrInvalid
		}
	default:
		return ErrInvalid
	}
	return nil
}

func jsonValueType(value reflect.Type) reflect.Type {
	unmarshaler := reflect.TypeFor[json.Unmarshaler]()
	for value != nil {
		if value.Implements(unmarshaler) || value.Kind() != reflect.Pointer && reflect.PointerTo(value).Implements(unmarshaler) {
			return nil
		}
		if value.Kind() != reflect.Pointer {
			return value
		}
		value = value.Elem()
	}
	return nil
}

func DecodePointer(reader io.Reader) (Pointer, error) {
	var pointer Pointer
	data, err := readBounded(reader, MaxPointerBytes)
	if err != nil {
		return pointer, err
	}
	if err := strictJSON(data, &pointer); err != nil {
		return pointer, err
	}
	return pointer, pointer.Validate()
}

func DecodeManifest(reader io.Reader, pointer Pointer) (Manifest, error) {
	var manifest Manifest
	if err := pointer.Validate(); err != nil {
		return manifest, err
	}
	data, err := readBounded(reader, MaxManifestBytes)
	if err != nil {
		return manifest, err
	}
	if Digest(data) != pointer.Digest {
		return manifest, ErrDigest
	}
	if err := strictJSON(data, &manifest); err != nil {
		return manifest, err
	}
	if manifest.Generation != pointer.Generation {
		return manifest, ErrChanged
	}
	return manifest, manifest.Validate()
}

func EncodeManifest(manifest Manifest) ([]byte, Pointer, error) {
	if err := manifest.Validate(); err != nil {
		return nil, Pointer{}, err
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		return nil, Pointer{}, ErrInvalid
	}
	if len(data) > MaxManifestBytes {
		return nil, Pointer{}, ErrLimit
	}
	return data, Pointer{Version: Version, Generation: manifest.Generation, Digest: Digest(data)}, nil
}
