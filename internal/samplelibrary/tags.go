package samplelibrary

import (
	"context"
	"encoding/binary"
	"io"
	"os"
	"strings"
)

const embeddedTagReadBudget int64 = 2 << 20

type ContentFacts struct {
	Title       string `json:"title,omitempty"`
	Artist      string `json:"artist,omitempty"`
	Album       string `json:"album,omitempty"`
	AlbumArtist string `json:"album_artist,omitempty"`
	Genre       string `json:"genre,omitempty"`
	Comment     string `json:"comment,omitempty"`
	Year        string `json:"year,omitempty"`
	Track       string `json:"track,omitempty"`
	Disc        string `json:"disc,omitempty"`
}
type tagReader func(context.Context, string, os.FileInfo) (ContentFacts, error)

var compiledTagReaders = map[string]tagReader{".mp3": readID3v2}

func readID3v2(ctx context.Context, path string, expected os.FileInfo) (ContentFacts, error) {
	file, err := openReadNoFollow(path)
	if err != nil {
		return ContentFacts{}, err
	}
	defer func() { _ = file.Close() }()
	current, err := file.Stat()
	if err != nil || !current.Mode().IsRegular() || !os.SameFile(expected, current) || current.Size() != expected.Size() {
		return ContentFacts{}, ErrRootChanged
	}
	header := make([]byte, 10)
	if _, err = io.ReadFull(&contextReader{ctx: ctx, reader: file}, header); err != nil {
		return ContentFacts{}, nil
	}
	if string(header[:3]) != "ID3" || header[3] < 2 || header[3] > 4 {
		return ContentFacts{}, nil
	}
	tagSize := syncSafe(header[6:10])
	if tagSize <= 0 || int64(tagSize) > embeddedTagReadBudget-10 {
		return ContentFacts{}, nil
	}
	data := make([]byte, tagSize)
	if _, err = io.ReadFull(&contextReader{ctx: ctx, reader: file}, data); err != nil {
		return ContentFacts{}, nil
	}
	facts := ContentFacts{}
	version := header[3]
	for offset := 0; offset+10 <= len(data); {
		id := string(data[offset : offset+4])
		if strings.Trim(id, "\x00") == "" {
			break
		}
		size := int(binary.BigEndian.Uint32(data[offset+4 : offset+8]))
		if version == 4 {
			size = syncSafe(data[offset+4 : offset+8])
		}
		offset += 10
		if size <= 0 || offset+size > len(data) {
			break
		}
		payload := data[offset : offset+size]
		offset += size
		value := ""
		if id == "COMM" {
			value = commentText(payload)
		} else {
			value = textFrame(payload)
		}
		switch id {
		case "TIT2":
			facts.Title = value
		case "TPE1":
			facts.Artist = value
		case "TALB":
			facts.Album = value
		case "TPE2":
			facts.AlbumArtist = value
		case "TCON":
			facts.Genre = value
		case "COMM":
			facts.Comment = value
		case "TDRC", "TYER":
			facts.Year = value
		case "TRCK":
			facts.Track = value
		case "TPOS":
			facts.Disc = value
		}
	}
	return normalizeFacts(facts), nil
}
func syncSafe(v []byte) int {
	if len(v) != 4 {
		return 0
	}
	for _, b := range v {
		if b&0x80 != 0 {
			return 0
		}
	}
	return int(v[0])<<21 | int(v[1])<<14 | int(v[2])<<7 | int(v[3])
}
func textFrame(payload []byte) string {
	if len(payload) < 2 {
		return ""
	}
	switch payload[0] {
	case 0, 3:
		return strings.TrimRight(string(payload[1:]), "\x00")
	default:
		return ""
	}
}
func commentText(payload []byte) string {
	if len(payload) < 5 {
		return ""
	}
	body := payload[4:]
	if index := strings.IndexByte(string(body), 0); index >= 0 {
		body = body[index+1:]
	}
	return strings.TrimRight(string(body), "\x00")
}
func normalizeFacts(v ContentFacts) ContentFacts {
	v.Title = sanitize(v.Title, 2000)
	v.Artist = sanitize(v.Artist, 2000)
	v.Album = sanitize(v.Album, 2000)
	v.AlbumArtist = sanitize(v.AlbumArtist, 2000)
	v.Genre = sanitize(v.Genre, 2000)
	v.Comment = sanitize(v.Comment, 2000)
	v.Year = sanitize(v.Year, 64)
	v.Track = sanitize(v.Track, 64)
	v.Disc = sanitize(v.Disc, 64)
	return v
}
