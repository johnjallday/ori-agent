package pluginapi

import (
	"testing"
)

func TestIsFileTypeAccepted(t *testing.T) {
	tests := []struct {
		name          string
		acceptedTypes []string
		filename      string
		mimeType      string
		want          bool
	}{
		{
			name:          "empty accepted types",
			acceptedTypes: []string{},
			filename:      "test.wav",
			mimeType:      "audio/wav",
			want:          false,
		},
		{
			name:          "match by extension",
			acceptedTypes: []string{".wav", ".mp3"},
			filename:      "drums.wav",
			mimeType:      "",
			want:          true,
		},
		{
			name:          "match by extension case insensitive",
			acceptedTypes: []string{".wav"},
			filename:      "DRUMS.WAV",
			mimeType:      "",
			want:          true,
		},
		{
			name:          "match by MIME type",
			acceptedTypes: []string{"audio/wav", "audio/mpeg"},
			filename:      "test.unknown",
			mimeType:      "audio/wav",
			want:          true,
		},
		{
			name:          "match by MIME type case insensitive",
			acceptedTypes: []string{"audio/wav"},
			filename:      "test.unknown",
			mimeType:      "AUDIO/WAV",
			want:          true,
		},
		{
			name:          "no match",
			acceptedTypes: []string{".wav", "audio/wav"},
			filename:      "test.mp3",
			mimeType:      "audio/mpeg",
			want:          false,
		},
		{
			name:          "match extension but not mime",
			acceptedTypes: []string{".wav"},
			filename:      "test.wav",
			mimeType:      "audio/mpeg",
			want:          true,
		},
		{
			name:          "match mime but not extension",
			acceptedTypes: []string{"audio/wav"},
			filename:      "test.mp3",
			mimeType:      "audio/wav",
			want:          true,
		},
		{
			name:          "zip file",
			acceptedTypes: []string{".zip", "application/zip"},
			filename:      "samples.zip",
			mimeType:      "application/zip",
			want:          true,
		},
		{
			name:          "midi file by extension",
			acceptedTypes: []string{".mid", ".midi"},
			filename:      "melody.mid",
			mimeType:      "",
			want:          true,
		},
		{
			name:          "midi file by mime type",
			acceptedTypes: []string{"audio/midi"},
			filename:      "melody.mid",
			mimeType:      "audio/midi",
			want:          true,
		},
		{
			name:          "file without extension",
			acceptedTypes: []string{".wav"},
			filename:      "noextension",
			mimeType:      "",
			want:          false,
		},
		{
			name:          "file without extension but with mime",
			acceptedTypes: []string{"audio/wav"},
			filename:      "noextension",
			mimeType:      "audio/wav",
			want:          true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsFileTypeAccepted(tt.acceptedTypes, tt.filename, tt.mimeType)
			if got != tt.want {
				t.Errorf("IsFileTypeAccepted(%v, %q, %q) = %v, want %v",
					tt.acceptedTypes, tt.filename, tt.mimeType, got, tt.want)
			}
		})
	}
}

func TestFilterFilesByAcceptedTypes(t *testing.T) {
	files := []FileAttachment{
		{Name: "drums.wav", Type: "audio/wav", Size: 1000, Content: []byte("wav data")},
		{Name: "bass.mp3", Type: "audio/mpeg", Size: 2000, Content: []byte("mp3 data")},
		{Name: "melody.mid", Type: "audio/midi", Size: 500, Content: []byte("midi data")},
		{Name: "samples.zip", Type: "application/zip", Size: 5000, Content: []byte("zip data")},
		{Name: "readme.txt", Type: "text/plain", Size: 100, Content: []byte("text data")},
	}

	tests := []struct {
		name          string
		acceptedTypes []string
		wantCount     int
		wantNames     []string
	}{
		{
			name:          "empty accepted types",
			acceptedTypes: []string{},
			wantCount:     0,
			wantNames:     nil,
		},
		{
			name:          "accept only wav",
			acceptedTypes: []string{".wav"},
			wantCount:     1,
			wantNames:     []string{"drums.wav"},
		},
		{
			name:          "accept audio files",
			acceptedTypes: []string{".wav", ".mp3", "audio/midi"},
			wantCount:     3,
			wantNames:     []string{"drums.wav", "bass.mp3", "melody.mid"},
		},
		{
			name:          "accept zip only",
			acceptedTypes: []string{"application/zip"},
			wantCount:     1,
			wantNames:     []string{"samples.zip"},
		},
		{
			name:          "accept all music files",
			acceptedTypes: []string{".wav", ".mp3", ".mid", ".zip"},
			wantCount:     4,
			wantNames:     []string{"drums.wav", "bass.mp3", "melody.mid", "samples.zip"},
		},
		{
			name:          "accept none matching",
			acceptedTypes: []string{".pdf", "image/png"},
			wantCount:     0,
			wantNames:     nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filtered := FilterFilesByAcceptedTypes(files, tt.acceptedTypes)

			if len(filtered) != tt.wantCount {
				t.Errorf("FilterFilesByAcceptedTypes() returned %d files, want %d",
					len(filtered), tt.wantCount)
				return
			}

			if tt.wantNames != nil {
				for i, f := range filtered {
					if f.Name != tt.wantNames[i] {
						t.Errorf("filtered[%d].Name = %q, want %q", i, f.Name, tt.wantNames[i])
					}
				}
			}
		})
	}
}

func TestToLower(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"HELLO", "hello"},
		{"Hello World", "hello world"},
		{"already lowercase", "already lowercase"},
		{"MiXeD CaSe", "mixed case"},
		{"", ""},
		{"123ABC", "123abc"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := toLower(tt.input)
			if got != tt.want {
				t.Errorf("toLower(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestLastIndex(t *testing.T) {
	tests := []struct {
		s    string
		c    byte
		want int
	}{
		{"hello.world.txt", '.', 11},
		{"noextension", '.', -1},
		{"a.b.c", '.', 3},
		{".hidden", '.', 0},
		{"", '.', -1},
	}

	for _, tt := range tests {
		t.Run(tt.s, func(t *testing.T) {
			got := lastIndex(tt.s, tt.c)
			if got != tt.want {
				t.Errorf("lastIndex(%q, %q) = %d, want %d", tt.s, tt.c, got, tt.want)
			}
		})
	}
}
