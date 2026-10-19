package backuprecovery

import (
	"bytes"
	"encoding/base64"
	"encoding/xml"
	"io"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	maxDiskMetadataBytes   = 64 << 10
	maxDiskMetadataTokens  = 8192
	maxDiskMetadataDepth   = 8
	maxDiskMetadataEntries = 256
	applePlistDoctype      = `DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd"`
)

var (
	diskDeviceReference    = regexp.MustCompile(`^disk[0-9]{1,6}(s[0-9]{1,6}){0,2}$`)
	diskPartitionReference = regexp.MustCompile(`^disk[0-9]{1,6}s[0-9]{1,6}$`)
)

// diskMetadata is private, untrusted OS evidence, not an identity or permission.
// The collector must join only explicitly referenced devices, check native mount
// evidence and compare the result before/after collection. Labels are discarded.
type diskMetadata struct {
	device, volumeID, partitionID, mountPoint            string
	filesystem, transport, physicalKind, parentWholeDisk string
	internal, whole, mounted, snapshot, mountedKnown     bool
	physicalStores                                       []string
}

// decodeDiskMetadata accepts the XML plist emitted by one scoped diskutil info
// operation, not diskutil list, arbitrary XML, or a browser-supplied observation.
// Process status/stderr, query deadlines and native device matching remain the
// collector's responsibility. No XML external entities or URLs are resolved.
func decodeDiskMetadata(raw []byte, expectedDevice string) (diskMetadata, error) {
	bad := func() (diskMetadata, error) { return diskMetadata{}, errStorageIdentity }
	if !diskDeviceReference.MatchString(expectedDevice) {
		return bad()
	}
	root, err := decodeDiskPlist(raw)
	if err != nil {
		return bad()
	}
	if _, ok := root.fields["Error"]; ok {
		return bad()
	}
	if _, ok := root.fields["ErrorMessage"]; ok {
		return bad()
	}
	var out diskMetadata
	stringsByKey := map[string]*string{
		"DeviceIdentifier": &out.device, "VolumeUUID": &out.volumeID, "DiskUUID": &out.partitionID,
		"MountPoint": &out.mountPoint, "FilesystemType": &out.filesystem, "BusProtocol": &out.transport,
		"VirtualOrPhysical": &out.physicalKind, "ParentWholeDisk": &out.parentWholeDisk,
	}
	for key, target := range stringsByKey {
		if value, present := root.fields[key]; present {
			if value.kind != "string" {
				return bad()
			}
			*target = value.text
		}
	}
	node, ok := root.fields["DeviceNode"]
	if out.device != expectedDevice || !ok || node.kind != "string" || node.text != "/dev/"+expectedDevice {
		return bad()
	}
	for key, target := range map[string]*bool{"Internal": &out.internal, "WholeDisk": &out.whole, "Mounted": &out.mounted, "APFSSnapshot": &out.snapshot} {
		value, present := root.fields[key]
		if !present {
			if key == "Internal" || key == "WholeDisk" {
				return bad()
			}
			continue
		}
		if value.kind != "true" && value.kind != "false" {
			return bad()
		}
		*target = value.kind == "true"
		if key == "Mounted" {
			out.mountedKnown = true
		}
	}
	for _, id := range []*string{&out.volumeID, &out.partitionID} {
		if *id != "" {
			normalized, err := normalizeStorageUUID(*id)
			if err != nil {
				return bad()
			}
			*id = normalized
		}
	}
	if out.parentWholeDisk != "" && !wholeDiskReference.MatchString(out.parentWholeDisk) {
		return bad()
	}
	if stores, present := root.fields["APFSPhysicalStores"]; present {
		if stores.kind != "array" || len(stores.items) < 1 || len(stores.items) > maxPhysicalStores {
			return bad()
		}
		seen := map[string]bool{}
		for _, store := range stores.items {
			if store.kind != "dict" {
				return bad()
			}
			value, ok := store.fields["APFSPhysicalStore"]
			if !ok || value.kind != "string" || !diskPartitionReference.MatchString(value.text) || seen[value.text] {
				return bad()
			}
			seen[value.text] = true
			out.physicalStores = append(out.physicalStores, value.text)
		}
	}
	return out, nil
}

type diskPlistValue struct {
	kind, text string
	fields     map[string]diskPlistValue
	items      []diskPlistValue
}

type diskPlistParser struct {
	decoder *xml.Decoder
	tokens  int
}

func (p *diskPlistParser) next() (xml.Token, error) {
	for {
		p.tokens++
		if p.tokens > maxDiskMetadataTokens {
			return nil, errStorageIdentity
		}
		token, err := p.decoder.Token()
		if err != nil {
			return nil, err
		}
		switch value := token.(type) {
		case xml.Comment:
			continue
		case xml.CharData:
			if len(bytes.TrimSpace(value)) == 0 {
				continue
			}
		}
		return token, nil
	}
}

func decodeDiskPlist(raw []byte) (diskPlistValue, error) {
	if len(raw) == 0 || len(raw) > maxDiskMetadataBytes || !utf8.Valid(raw) {
		return diskPlistValue{}, errStorageIdentity
	}
	p := diskPlistParser{decoder: xml.NewDecoder(bytes.NewReader(raw))}
	var token xml.Token
	var err error
	declaration, doctype := false, false
	for {
		token, err = p.next()
		if err != nil {
			return diskPlistValue{}, errStorageIdentity
		}
		switch value := token.(type) {
		case xml.ProcInst:
			if declaration || doctype || value.Target != "xml" || string(value.Inst) != `version="1.0" encoding="UTF-8"` {
				return diskPlistValue{}, errStorageIdentity
			}
			declaration = true
			continue
		case xml.Directive:
			if doctype || string(value) != applePlistDoctype {
				return diskPlistValue{}, errStorageIdentity
			}
			doctype = true
			continue
		}
		break
	}
	start, ok := token.(xml.StartElement)
	if !ok || start.Name != (xml.Name{Local: "plist"}) || len(start.Attr) != 1 || start.Attr[0].Name != (xml.Name{Local: "version"}) || start.Attr[0].Value != "1.0" {
		return diskPlistValue{}, errStorageIdentity
	}
	token, err = p.next()
	if err != nil {
		return diskPlistValue{}, errStorageIdentity
	}
	value, err := p.value(token, 1)
	if err != nil || value.kind != "dict" {
		return diskPlistValue{}, errStorageIdentity
	}
	token, err = p.next()
	end, ok := token.(xml.EndElement)
	if err != nil || !ok || end.Name != start.Name {
		return diskPlistValue{}, errStorageIdentity
	}
	if _, err = p.next(); err != io.EOF {
		return diskPlistValue{}, errStorageIdentity
	}
	return value, nil
}

func (p *diskPlistParser) value(token xml.Token, depth int) (diskPlistValue, error) {
	bad := func() (diskPlistValue, error) { return diskPlistValue{}, errStorageIdentity }
	start, ok := token.(xml.StartElement)
	if !ok || depth > maxDiskMetadataDepth || start.Name.Space != "" || len(start.Attr) != 0 {
		return bad()
	}
	out := diskPlistValue{kind: start.Name.Local}
	if out.kind == "dict" || out.kind == "array" {
		if out.kind == "dict" {
			out.fields = map[string]diskPlistValue{}
		}
		for count := 0; ; count++ {
			token, err := p.next()
			if err != nil {
				return bad()
			}
			if end, ok := token.(xml.EndElement); ok {
				if end.Name != start.Name {
					return bad()
				}
				return out, nil
			}
			if count >= maxDiskMetadataEntries {
				return bad()
			}
			key := ""
			if out.kind == "dict" {
				k, err := p.value(token, depth+1)
				if err != nil || k.kind != "key" || k.text == "" || len(k.text) > 128 {
					return bad()
				}
				key = k.text
				if _, present := out.fields[key]; present {
					return bad()
				}
				token, err = p.next()
				if err != nil {
					return bad()
				}
			}
			child, err := p.value(token, depth+1)
			if err != nil || child.kind == "key" {
				return bad()
			}
			if out.kind == "dict" {
				out.fields[key] = child
			} else {
				out.items = append(out.items, child)
			}
		}
	}
	// Scalar content must be text only. Token accounting includes whitespace
	// and comments, so ignored fields cannot escape the parser's resource cap.
	var text strings.Builder
	for {
		p.tokens++
		if p.tokens > maxDiskMetadataTokens {
			return bad()
		}
		token, err := p.decoder.Token()
		if err != nil {
			return bad()
		}
		switch value := token.(type) {
		case xml.CharData:
			if text.Len()+len(value) > maxSourcePathBytes {
				return bad()
			}
			_, _ = text.Write(value) // strings.Builder.Write cannot fail.
		case xml.EndElement:
			if value.Name != start.Name {
				return bad()
			}
			out.text = text.String()
			if !validDiskPlistScalar(out) {
				return bad()
			}
			return out, nil
		default:
			return bad()
		}
	}
}

func validDiskPlistScalar(value diskPlistValue) bool {
	switch value.kind {
	case "string", "key":
		return !strings.ContainsRune(value.text, '\ufffd')
	case "true", "false":
		return value.text == ""
	case "integer":
		if _, err := strconv.ParseInt(value.text, 10, 64); err == nil {
			return true
		}
		_, err := strconv.ParseUint(value.text, 10, 64)
		return err == nil
	case "real":
		v, err := strconv.ParseFloat(value.text, 64)
		return err == nil && !math.IsInf(v, 0) && !math.IsNaN(v)
	case "date":
		_, err := time.Parse(time.RFC3339, value.text)
		return err == nil
	case "data":
		_, err := base64.StdEncoding.DecodeString(strings.Join(strings.Fields(value.text), ""))
		return err == nil
	default:
		return false
	}
}
