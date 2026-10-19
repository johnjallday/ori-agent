package backuprecovery

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"math"
	"strconv"
	"time"
	"unicode/utf16"
	"unicode/utf8"
)

// Time Machine keeps its preferences as a binary property list. This is a
// bounded reader for exactly the bplist00 shapes those preferences use, into
// the same value tree as the XML reader. It executes nothing and follows no
// reference outside the buffer; anything unexpected is unavailable.
const (
	maxTimeMachinePlistBytes   = 2 << 20
	maxTimeMachinePlistObjects = 1 << 16
	maxTimeMachinePlistDepth   = 16
)

var errTimeMachinePlist = errors.New("time machine metadata is unavailable")

// appleEpoch is the reference date of binary plist dates.
var appleEpoch = time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC)

// decodeTimeMachinePlist accepts an XML or binary property list whose root is
// a dictionary.
func decodeTimeMachinePlist(raw []byte) (diskPlistValue, error) {
	if bytes.HasPrefix(raw, []byte("bplist00")) {
		return decodeBinaryPlist(raw)
	}
	value, err := decodeDiskPlist(raw)
	if err != nil {
		return diskPlistValue{}, errTimeMachinePlist
	}
	return value, nil
}

type binaryPlist struct {
	raw        []byte
	offsets    []uint64
	refSize    int
	visits     int
	tableStart uint64
}

func decodeBinaryPlist(raw []byte) (diskPlistValue, error) {
	p, top, err := openBinaryPlist(raw, maxTimeMachinePlistObjects)
	if err != nil {
		return diskPlistValue{}, err
	}
	value, err := p.object(top, 0)
	if err != nil || value.kind != "dict" {
		return diskPlistValue{}, errTimeMachinePlist
	}
	return value, nil
}

// openBinaryPlist validates the header, trailer and offset table, so every
// later read stays inside the object area.
func openBinaryPlist(raw []byte, maxObjects uint64) (*binaryPlist, uint64, error) {
	if len(raw) < 8+32 || len(raw) > maxTimeMachinePlistBytes || !bytes.HasPrefix(raw, []byte("bplist00")) {
		return nil, 0, errTimeMachinePlist
	}
	trailer := raw[len(raw)-32:]
	offsetSize, refSize := int(trailer[6]), int(trailer[7])
	count := binary.BigEndian.Uint64(trailer[8:16])
	top := binary.BigEndian.Uint64(trailer[16:24])
	table := binary.BigEndian.Uint64(trailer[24:32])
	if !validPlistIntSize(offsetSize) || !validPlistIntSize(refSize) || count == 0 || count > maxObjects || top >= count ||
		table < 8 || table > uint64(len(raw)-32) || count*uint64(offsetSize) > uint64(len(raw)-32)-table {
		return nil, 0, errTimeMachinePlist
	}
	p := &binaryPlist{raw: raw, refSize: refSize, tableStart: table, offsets: make([]uint64, count)}
	for i := range p.offsets {
		start := table + uint64(i*offsetSize)
		p.offsets[i] = readPlistUint(raw[start : start+uint64(offsetSize)])
		if p.offsets[i] < 8 || p.offsets[i] >= table {
			return nil, 0, errTimeMachinePlist
		}
	}
	return p, top, nil
}

func validPlistIntSize(size int) bool { return size == 1 || size == 2 || size == 4 || size == 8 }

func readPlistUint(b []byte) uint64 {
	var v uint64
	for _, c := range b {
		v = v<<8 | uint64(c)
	}
	return v
}

// span returns raw[start:start+n] within the object area, or false.
func (p *binaryPlist) span(start, n uint64) ([]byte, bool) {
	if start > p.tableStart || n > p.tableStart-start {
		return nil, false
	}
	return p.raw[start : start+n], true
}

// length reads an object's length: the marker's low nibble, or an integer
// object that follows when the nibble is 0xF.
func (p *binaryPlist) length(at uint64, nibble byte) (n, next uint64, ok bool) {
	if nibble != 0x0F {
		return uint64(nibble), at + 1, true
	}
	head, ok := p.span(at+1, 1)
	if !ok || head[0]>>4 != 0x1 || head[0]&0x0F > 3 {
		return 0, 0, false
	}
	size := uint64(1) << (head[0] & 0x0F)
	body, ok := p.span(at+2, size)
	if !ok {
		return 0, 0, false
	}
	n = readPlistUint(body)
	return n, at + 2 + size, n <= maxTimeMachinePlistBytes
}

func (p *binaryPlist) object(index uint64, depth int) (diskPlistValue, error) {
	bad := func() (diskPlistValue, error) { return diskPlistValue{}, errTimeMachinePlist }
	p.visits++
	if index >= uint64(len(p.offsets)) || depth > maxTimeMachinePlistDepth || p.visits > maxTimeMachinePlistObjects {
		return bad()
	}
	at := p.offsets[index]
	marker, ok := p.span(at, 1)
	if !ok {
		return bad()
	}
	kind, nibble := marker[0]>>4, marker[0]&0x0F
	switch kind {
	case 0x0:
		switch marker[0] {
		case 0x08:
			return diskPlistValue{kind: "false"}, nil
		case 0x09:
			return diskPlistValue{kind: "true"}, nil
		}
		return bad()
	case 0x1:
		if nibble > 3 {
			return bad()
		}
		body, ok := p.span(at+1, 1<<nibble)
		if !ok {
			return bad()
		}
		v := readPlistUint(body)
		if nibble == 3 {
			return diskPlistValue{kind: "integer", text: strconv.FormatInt(int64(v), 10)}, nil
		}
		return diskPlistValue{kind: "integer", text: strconv.FormatUint(v, 10)}, nil
	case 0x2, 0x3:
		if nibble != 2 && nibble != 3 || kind == 0x3 && marker[0] != 0x33 {
			return bad()
		}
		body, ok := p.span(at+1, 1<<nibble)
		if !ok {
			return bad()
		}
		v := float64(math.Float32frombits(uint32(readPlistUint(body))))
		if nibble == 3 {
			v = math.Float64frombits(readPlistUint(body))
		}
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return bad()
		}
		if kind == 0x2 {
			return diskPlistValue{kind: "real", text: strconv.FormatFloat(v, 'g', -1, 64)}, nil
		}
		if math.Abs(v) > 1e11 {
			return bad()
		}
		stamp := appleEpoch.Add(time.Duration(v * float64(time.Second))).UTC().Truncate(time.Second)
		return diskPlistValue{kind: "date", text: stamp.Format(time.RFC3339)}, nil
	case 0x4, 0x5, 0x6:
		n, start, ok := p.length(at, nibble)
		if !ok {
			return bad()
		}
		width := uint64(1)
		if kind == 0x6 {
			width = 2
		}
		body, ok := p.span(start, n*width)
		if !ok {
			return bad()
		}
		switch kind {
		case 0x4:
			return diskPlistValue{kind: "data", text: base64.StdEncoding.EncodeToString(body)}, nil
		case 0x5:
			for _, c := range body {
				if c >= 0x80 {
					return bad()
				}
			}
			return plistString(string(body))
		default:
			units := make([]uint16, n)
			for i := range units {
				units[i] = binary.BigEndian.Uint16(body[2*i:])
			}
			return plistString(string(utf16.Decode(units)))
		}
	case 0xA, 0xD:
		n, start, ok := p.length(at, nibble)
		if !ok || n > 4096 {
			return bad()
		}
		refs := n
		if kind == 0xD {
			refs = 2 * n
		}
		body, ok := p.span(start, refs*uint64(p.refSize))
		if !ok {
			return bad()
		}
		ref := func(i uint64) uint64 {
			return readPlistUint(body[i*uint64(p.refSize) : (i+1)*uint64(p.refSize)])
		}
		if kind == 0xA {
			out := diskPlistValue{kind: "array"}
			for i := uint64(0); i < n; i++ {
				child, err := p.object(ref(i), depth+1)
				if err != nil {
					return bad()
				}
				out.items = append(out.items, child)
			}
			return out, nil
		}
		out := diskPlistValue{kind: "dict", fields: map[string]diskPlistValue{}}
		for i := uint64(0); i < n; i++ {
			key, err := p.object(ref(i), depth+1)
			if err != nil || key.kind != "string" || key.text == "" || len(key.text) > 128 {
				return bad()
			}
			if _, present := out.fields[key.text]; present {
				return bad()
			}
			child, err := p.object(ref(n+i), depth+1)
			if err != nil {
				return bad()
			}
			out.fields[key.text] = child
		}
		return out, nil
	}
	return bad()
}

func plistString(text string) (diskPlistValue, error) {
	if !utf8.ValidString(text) || len(text) > maxSourcePathBytes {
		return diskPlistValue{}, errTimeMachinePlist
	}
	return diskPlistValue{kind: "string", text: text}, nil
}
