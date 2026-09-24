package folderdigest

import (
	"fmt"
	"strings"
	"time"
)

// extensionKinds turns an extension into the plain word a reason line uses
// ("14 LaTeX files"). Anything missing falls back to the upper-cased
// extension ("3 MOV files").
var extensionKinds = map[string]string{
	".tex": "LaTeX", ".bib": "BibTeX", ".pdf": "PDF", ".docx": "Word", ".doc": "Word",
	".md": "Markdown", ".txt": "text", ".rtf": "rich text", ".epub": "EPUB",
	".xlsx": "Excel", ".pptx": "PowerPoint", ".csv": "CSV", ".json": "JSON",
	".xml": "XML", ".html": "HTML", ".yaml": "YAML", ".yml": "YAML",
	".go": "Go", ".py": "Python", ".js": "JavaScript", ".ts": "TypeScript",
	".swift": "Swift", ".rs": "Rust", ".java": "Java", ".rb": "Ruby", ".c": "C",
	".h": "C header", ".css": "CSS", ".sh": "shell", ".ipynb": "notebook",
	".rpp": "REAPER", ".wav": "WAV", ".aif": "AIFF", ".aiff": "AIFF", ".mp3": "MP3",
	".flac": "FLAC", ".mid": "MIDI", ".jpg": "JPEG", ".jpeg": "JPEG", ".png": "PNG",
	".heic": "HEIC", ".gif": "GIF", ".svg": "SVG", ".psd": "Photoshop", ".fig": "Figma",
	".mov": "video", ".mp4": "video", ".zip": "ZIP", ".dmg": "disk image", ".pkg": "installer",
}

// KindName is the plain word for a file extension.
func KindName(ext string) string {
	if ext == "" {
		return "untyped"
	}
	if name, ok := extensionKinds[strings.ToLower(ext)]; ok {
		return name
	}
	return strings.ToUpper(strings.TrimPrefix(ext, "."))
}

// reasonFor builds the one-line reason from counts and kinds only (FR19).
func reasonFor(v Verdict, r Result, now time.Time) string {
	var line string
	switch v.Kind {
	case KindProject:
		line = describeWork(*v.Project, now)
	case KindDump:
		line = fmt.Sprintf("%s of %s", plural(v.LooseFiles, "loose file"), plural(v.LooseKinds, "kind"))
	case KindMixed:
		line = fmt.Sprintf("%s and %s", plural(len(v.Projects), "project"), plural(v.LooseFiles, "loose file"))
	case KindEmpty:
		if v.Root.FileCount == 0 {
			line = "no files"
		} else {
			line = plural(v.Root.FileCount, "file")
		}
	default:
		line = describeWork(v.Root, now)
	}
	if r.SkippedLinks > 0 {
		line += ", " + plural(r.SkippedLinks, "skipped link")
	}
	if v.Partial {
		line += " (partial look)"
	}
	return line
}

// describeWork says what a candidate holds and when it was last touched:
// "14 LaTeX files, edited yesterday" or "31 PDF files, last edited in March".
func describeWork(c Candidate, now time.Time) string {
	var what string
	switch {
	case c.FileCount == 0:
		what = "no files"
	case c.DominantShare >= 0.5:
		what = plural(c.DominantCount, KindName(c.DominantExtension)+" file")
	case c.HasMarker():
		what = fmt.Sprintf("%s with %s", c.Marker.Label, plural(c.FileCount, "file"))
	default:
		what = fmt.Sprintf("%s of %s", plural(c.FileCount, "file"), plural(c.DistinctExtensions(), "kind"))
	}
	if c.NewestModTime.IsZero() {
		return what
	}
	return what + ", " + describeWhen(c.NewestModTime, now)
}

// describeWhen words a modification time relative to now. Recent edits are
// counted in days; older ones name the month, with the year when it is not
// the current one.
func describeWhen(t, now time.Time) string {
	days := int(now.Sub(t).Hours() / 24)
	switch {
	case days <= 0:
		return "edited today"
	case days == 1:
		return "edited yesterday"
	case days <= 30:
		return fmt.Sprintf("edited %d days ago", days)
	case t.Year() == now.Year():
		return "last edited in " + t.Month().String()
	default:
		return fmt.Sprintf("last edited in %s %d", t.Month().String(), t.Year())
	}
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
