package folderdigest

import (
	"fmt"
	"sort"
	"time"
)

// Thresholds behind the verdicts (FR16–FR18). They are starting points,
// expected to be tuned; each carries the reasoning it began with.
const (
	// ProjectMinFiles is the smallest file count the density rule accepts.
	// Eight files is a body of work rather than a few stray exports.
	ProjectMinFiles = 8
	// ProjectDominantShare is the share of files the dominant extension must
	// reach for a folder to look like one kind of work in progress.
	ProjectDominantShare = 0.60
	// ProjectRecentWindow is how recently the folder must have been edited
	// for the density rule. Thirty days separates current work from an
	// archive that happens to be tidy.
	ProjectRecentWindow = 30 * 24 * time.Hour
	// DumpMinLooseFiles is the smallest number of files sitting directly in
	// the root that makes it look like a dump.
	DumpMinLooseFiles = 20
	// DumpMinKinds is the smallest number of distinct extensions among those
	// loose files. Twenty screenshots are a collection; twenty files of five
	// kinds are a landing zone.
	DumpMinKinds = 5
	// EmptyMaxFiles: a root with fewer files than this has nothing for the
	// assistant to do yet.
	EmptyMaxFiles = 3
)

// documentExtensions are exempt from the density rule (FR16): a folder of
// thirty scanned invoices is not a project just because it is uniform and
// recent. Only a marker makes such a folder a project signal.
var documentExtensions = map[string]bool{
	".pdf": true, ".docx": true, ".doc": true, ".epub": true, ".xlsx": true,
	".jpg": true, ".jpeg": true, ".png": true, ".heic": true,
}

// IsDocumentExtension reports whether ext (with the dot) is one of the
// document or image kinds the density rule ignores.
func IsDocumentExtension(ext string) bool { return documentExtensions[ext] }

// Kind is the verdict on a root.
type Kind string

const (
	KindProject   Kind = "project"
	KindDump      Kind = "dump"
	KindMixed     Kind = "mixed"
	KindAmbiguous Kind = "ambiguous"
	KindEmpty     Kind = "empty"
	// KindDeclined is not a shape: it is what remains when the user has
	// already said "not this one" about the root and nothing inside it is
	// left to offer (FR23).
	KindDeclined Kind = "declined"
)

// Verdict is what the assistant concluded about a root and why.
type Verdict struct {
	Kind Kind
	// Reason is the one-line explanation shown under every offer (FR19).
	Reason string
	// Root is the root candidate's stats.
	Root Candidate
	// RootDump records whether the root met the dump rule (FR17), kept so the
	// verdict can be re-decided after declined candidates are removed.
	RootDump bool
	// Project is the candidate the offer names: the root or the top-ranked
	// subfolder. Nil unless Kind is project or mixed.
	Project *Candidate
	// Projects are every project-signal candidate in rank order (FR20). The
	// first is Project; the rest are kept for later offers.
	Projects []Candidate
	// LooseFiles and LooseKinds describe the root's direct children, for the
	// tidy branch of a mixed offer.
	LooseFiles int
	LooseKinds int
	Partial    bool
}

// LooseReason words the root's loose files the way a dump offer does
// ("40 loose files of 6 kinds"), for the tidy branch of a mixed offer.
func (v Verdict) LooseReason() string {
	return fmt.Sprintf("%s of %s", plural(v.LooseFiles, "loose file"), plural(v.LooseKinds, "kind"))
}

// DescribeCandidate words one candidate's contents and last edit, the way a
// project offer's reason does ("14 LaTeX files, edited yesterday").
func DescribeCandidate(c Candidate, now time.Time) string {
	return describeWork(c, now)
}

// projectSignal applies FR16 to one candidate.
func projectSignal(c Candidate, now time.Time) bool {
	if c.HasMarker() {
		return true
	}
	if c.DominantExtension == "" || IsDocumentExtension(c.DominantExtension) {
		return false
	}
	if c.FileCount < ProjectMinFiles || c.DominantShare < ProjectDominantShare {
		return false
	}
	return !c.NewestModTime.IsZero() && now.Sub(c.NewestModTime) <= ProjectRecentWindow
}

// dumpSignal applies FR17 to the root.
func dumpSignal(root Candidate) bool {
	return !root.HasMarker() && root.LooseFiles >= DumpMinLooseFiles && root.LooseKinds() >= DumpMinKinds
}

// Rank orders project-signal candidates: marker first, then most recent
// modification, then file count (FR20). Name breaks the remaining ties so
// the order is stable.
func Rank(candidates []Candidate) []Candidate {
	ranked := append([]Candidate{}, candidates...)
	sort.SliceStable(ranked, func(i, j int) bool {
		a, b := ranked[i], ranked[j]
		if a.HasMarker() != b.HasMarker() {
			return a.HasMarker()
		}
		if !a.NewestModTime.Equal(b.NewestModTime) {
			return a.NewestModTime.After(b.NewestModTime)
		}
		if a.FileCount != b.FileCount {
			return a.FileCount > b.FileCount
		}
		return a.Name < b.Name
	})
	return ranked
}

// Decide turns a scan result into a verdict (FR18). The conditions are
// checked in the order the requirement lists them: project, then mixed and
// dump, then empty, then ambiguous.
func Decide(r Result, now time.Time) Verdict {
	return decide(r, now, func(Candidate) bool { return false })
}

// Exclude re-decides the verdict without the candidates the user declined
// earlier (FR23): a declined subfolder is no longer a project signal, and a
// declined root is neither a project nor a dump, leaving only what sits
// inside it to offer.
func Exclude(r Result, now time.Time, declined func(c Candidate) bool) Verdict {
	if declined == nil {
		return Decide(r, now)
	}
	return decide(r, now, declined)
}

func decide(r Result, now time.Time, declined func(Candidate) bool) Verdict {
	root := r.RootCandidate()
	v := Verdict{
		Root:       root,
		LooseFiles: root.LooseFiles,
		LooseKinds: root.LooseKinds(),
		Partial:    r.Partial,
	}

	var subProjects []Candidate
	for _, c := range r.Subfolders() {
		if projectSignal(c, now) && !declined(c) {
			subProjects = append(subProjects, c)
		}
	}
	subProjects = Rank(subProjects)
	rootDeclined := declined(root)
	rootProject := !rootDeclined && projectSignal(root, now)
	v.RootDump = !rootDeclined && dumpSignal(root)

	switch {
	case rootProject:
		v.Kind = KindProject
		v.Projects = append([]Candidate{root}, subProjects...)
	case v.RootDump && len(subProjects) >= 1, len(subProjects) >= 2:
		v.Kind = KindMixed
		v.Projects = subProjects
	case len(subProjects) == 1:
		v.Kind = KindProject
		v.Projects = subProjects
	case v.RootDump:
		v.Kind = KindDump
	case rootDeclined:
		v.Kind = KindDeclined
	case root.FileCount < EmptyMaxFiles:
		v.Kind = KindEmpty
	default:
		v.Kind = KindAmbiguous
	}
	if len(v.Projects) > 0 {
		top := v.Projects[0]
		v.Project = &top
	}
	v.Reason = reasonFor(v, r, now)
	return v
}

// ShapeBlueprint is one row of the shape → blueprint table in tables.go.
type ShapeBlueprint struct {
	Shape       Shape
	BlueprintID string
	// Label is the blueprint's display name, for the fallback note when it
	// is not installed.
	Label string
}

// BlueprintForShape returns the preferred blueprint for a shape, if the
// table names one. Shapes without a row start from the blank workspace.
func BlueprintForShape(shape Shape) (ShapeBlueprint, bool) {
	for _, row := range ShapeBlueprints {
		if row.Shape == shape {
			return row, true
		}
	}
	return ShapeBlueprint{}, false
}

// ShapeFor names the shape a project outcome should use for a candidate
// (FR28): the marker's shape when it has one, otherwise a document shape
// inferred from the dominant file kind, otherwise "" for the generic
// project blueprint.
func ShapeFor(c Candidate) Shape {
	if c.Marker != nil {
		return c.Marker.Shape
	}
	switch c.DominantExtension {
	case ".tex", ".md", ".docx":
		return ShapeManuscript
	case ".pdf", ".epub":
		return ShapeCorpus
	}
	return ""
}
