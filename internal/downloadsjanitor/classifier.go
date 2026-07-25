package downloadsjanitor

import (
	"fmt"
	"strings"
)

// The deterministic classifier maps a file's metadata to a category. It runs
// first for every candidate and never opens a file — extension, detected type,
// name shape, size, and timestamps are all it sees (FR-45, FR-46).
//
// Two properties are deliberate:
//
//   - It is a table, not a heuristic. The same file always lands in the same
//     category, and the reason shown to the user is the actual rule that fired,
//     not a post-hoc explanation.
//   - Anything it cannot place confidently goes to Other and is marked
//     "Needs review" rather than being pushed into a plausible-looking category.
//     A wrong-but-confident guess costs the user more than an honest one.

// extensionCategories maps a lower-cased extension (with dot) to its category.
// Compound extensions like ".tar.gz" are handled by compoundExtensions.
var extensionCategories = map[string]Category{
	// Documents
	".pdf": CategoryDocuments, ".doc": CategoryDocuments, ".docx": CategoryDocuments,
	".odt": CategoryDocuments, ".rtf": CategoryDocuments, ".txt": CategoryDocuments,
	".md": CategoryDocuments, ".pages": CategoryDocuments, ".epub": CategoryDocuments,
	".mobi": CategoryDocuments, ".azw3": CategoryDocuments, ".tex": CategoryDocuments,
	".ppt": CategoryDocuments, ".pptx": CategoryDocuments, ".key": CategoryDocuments,
	".odp": CategoryDocuments, ".xls": CategoryDocuments, ".xlsx": CategoryDocuments,
	".numbers": CategoryDocuments, ".ods": CategoryDocuments,

	// Images
	".jpg": CategoryImages, ".jpeg": CategoryImages, ".png": CategoryImages,
	".gif": CategoryImages, ".bmp": CategoryImages, ".tif": CategoryImages,
	".tiff": CategoryImages, ".webp": CategoryImages, ".heic": CategoryImages,
	".heif": CategoryImages, ".svg": CategoryImages, ".ico": CategoryImages,
	".raw": CategoryImages, ".cr2": CategoryImages, ".nef": CategoryImages,
	".dng": CategoryImages, ".psd": CategoryImages, ".ai": CategoryImages,
	".avif": CategoryImages,

	// Audio
	".mp3": CategoryAudio, ".wav": CategoryAudio, ".aiff": CategoryAudio,
	".aif": CategoryAudio, ".flac": CategoryAudio, ".m4a": CategoryAudio,
	".aac": CategoryAudio, ".ogg": CategoryAudio, ".opus": CategoryAudio,
	".wma": CategoryAudio, ".mid": CategoryAudio, ".midi": CategoryAudio,

	// Video
	".mp4": CategoryVideo, ".m4v": CategoryVideo, ".mov": CategoryVideo,
	".avi": CategoryVideo, ".mkv": CategoryVideo, ".webm": CategoryVideo,
	".wmv": CategoryVideo, ".flv": CategoryVideo, ".mpg": CategoryVideo,
	".mpeg": CategoryVideo, ".m2ts": CategoryVideo,

	// Archives
	".zip": CategoryArchives, ".tar": CategoryArchives, ".gz": CategoryArchives,
	".tgz": CategoryArchives, ".bz2": CategoryArchives, ".xz": CategoryArchives,
	".7z": CategoryArchives, ".rar": CategoryArchives, ".zst": CategoryArchives,
	".iso": CategoryArchives, ".img": CategoryArchives,

	// Installers
	".dmg": CategoryInstallers, ".pkg": CategoryInstallers, ".msi": CategoryInstallers,
	".exe": CategoryInstallers, ".deb": CategoryInstallers, ".rpm": CategoryInstallers,
	".appimage": CategoryInstallers, ".apk": CategoryInstallers, ".msix": CategoryInstallers,

	// Data
	".csv": CategoryData, ".tsv": CategoryData, ".json": CategoryData,
	".xml": CategoryData, ".yaml": CategoryData, ".yml": CategoryData,
	".sql": CategoryData, ".parquet": CategoryData, ".ndjson": CategoryData,
	".sqlite": CategoryData, ".db": CategoryData, ".ics": CategoryData,
	".vcf": CategoryData, ".geojson": CategoryData,
}

// compoundExtensions are two-part extensions whose meaning comes from the pair.
// Checked before the single-extension table so "backup.tar.gz" is an archive
// rather than being decided by ".gz" alone.
var compoundExtensions = map[string]Category{
	".tar.gz":  CategoryArchives,
	".tar.bz2": CategoryArchives,
	".tar.xz":  CategoryArchives,
	".tar.zst": CategoryArchives,
}

// genericExtensions are extensions that exist but say nothing useful about
// content. They are recognized so the reason can say so, but they still route
// to Other for review rather than to a guessed category.
var genericExtensions = map[string]struct{}{
	".bin": {}, ".dat": {}, ".tmp": {}, ".part": {}, ".out": {},
	".bak": {}, ".old": {}, ".cache": {}, ".lock": {},
}

// mimeMajorCategories maps a MIME major type to a category, used when the
// extension is unknown but the type is not.
var mimeMajorCategories = map[string]Category{
	"image": CategoryImages,
	"audio": CategoryAudio,
	"video": CategoryVideo,
}

// Classification is one deterministic verdict.
type Classification struct {
	Category    Category
	Reason      string
	Confidence  ConfidenceBand
	Score       float64
	Classifier  ClassifierKind
	NeedsReview bool
	// Ambiguous marks a candidate the deterministic pass could not place. These
	// are the only ones a configured model is later asked about, and only when
	// the user has enabled it.
	Ambiguous bool
}

// ClassifyMetadata places a candidate using metadata alone.
func ClassifyMetadata(candidate JanitorCandidate) Classification {
	name := strings.ToLower(strings.TrimSpace(candidate.Name))
	extension := strings.ToLower(strings.TrimSpace(candidate.Extension))
	if extension == "" {
		extension = extensionOf(name)
	}

	if category, ok := compoundExtensionFor(name); ok {
		return Classification{
			Category:   category,
			Reason:     fmt.Sprintf("%s archive", strings.TrimPrefix(compoundExtensionSuffix(name), ".")),
			Confidence: ConfidenceHigh,
			Score:      0.95,
			Classifier: ClassifierMetadata,
		}
	}

	if extension == "" {
		return ambiguous("No file extension to go on", candidate)
	}

	if _, generic := genericExtensions[extension]; generic {
		return ambiguous(fmt.Sprintf("%s files can hold anything", extension), candidate)
	}

	if category, ok := extensionCategories[extension]; ok {
		return Classification{
			Category:   category,
			Reason:     fmt.Sprintf("%s file", strings.TrimPrefix(extension, ".")),
			Confidence: ConfidenceHigh,
			Score:      0.9,
			Classifier: ClassifierMetadata,
		}
	}

	// The extension is unrecognized, but a detected type may still be decisive.
	// This is a weaker signal — the type was derived from the same name — so the
	// verdict carries medium confidence and says where it came from.
	if major, _, ok := strings.Cut(strings.TrimSpace(candidate.MIMEType), "/"); ok {
		if category, known := mimeMajorCategories[strings.ToLower(major)]; known {
			return Classification{
				Category:   category,
				Reason:     fmt.Sprintf("Detected as %s", candidate.MIMEType),
				Confidence: ConfidenceMedium,
				Score:      0.6,
				Classifier: ClassifierMetadata,
			}
		}
		if strings.EqualFold(major, "text") {
			return Classification{
				Category:   CategoryDocuments,
				Reason:     fmt.Sprintf("Detected as %s", candidate.MIMEType),
				Confidence: ConfidenceMedium,
				Score:      0.6,
				Classifier: ClassifierMetadata,
			}
		}
	}

	return ambiguous(fmt.Sprintf("Unrecognized %s file", strings.TrimPrefix(extension, ".")), candidate)
}

// ambiguous builds the honest fallback: Other, low confidence, flagged for the
// user, and marked so a model may be asked about it later if one is configured.
func ambiguous(reason string, _ JanitorCandidate) Classification {
	return Classification{
		Category:    CategoryOther,
		Reason:      reason,
		Confidence:  ConfidenceLow,
		Score:       0.2,
		Classifier:  ClassifierFallback,
		NeedsReview: true,
		Ambiguous:   true,
	}
}

// Apply writes a classification onto a candidate.
func (c Classification) Apply(candidate *JanitorCandidate) {
	if candidate == nil {
		return
	}
	candidate.Category = c.Category
	candidate.Reason = c.Reason
	candidate.Confidence = c.Confidence
	candidate.ConfidenceScore = c.Score
	candidate.Classifier = c.Classifier
	candidate.NeedsReview = c.NeedsReview
}

// extensionOf returns the lower-cased extension of a filename, or "" when there
// is none. A leading dot (a hidden file) is not an extension — but hidden files
// never reach the classifier, since the scanner filters them first.
func extensionOf(name string) string {
	idx := strings.LastIndex(name, ".")
	if idx <= 0 || idx == len(name)-1 {
		return ""
	}
	return strings.ToLower(name[idx:])
}

func compoundExtensionSuffix(name string) string {
	for suffix := range compoundExtensions {
		if strings.HasSuffix(name, suffix) {
			return suffix
		}
	}
	return ""
}

func compoundExtensionFor(name string) (Category, bool) {
	suffix := compoundExtensionSuffix(name)
	if suffix == "" {
		return "", false
	}
	category, ok := compoundExtensions[suffix]
	return category, ok
}
