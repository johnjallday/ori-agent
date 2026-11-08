// Package foldermap is the host's walk of a folder fed to Ori: a
// breadth-first, metadata-only listing under an entry and time budget, the
// folder map it records, and the facts computed from that map. Nothing here
// opens a file, follows a symbolic link or looks outside the fed folder, and
// no constant below is a depth: how far the walk goes is bounded only by the
// budgets and by which folders hold projects.
//
// Recognition stays in internal/folderdigest's host tables (markers, shapes,
// capability rows, skipped folders, dump thresholds); this package only
// applies them at any depth.
package foldermap

import (
	"time"

	"github.com/johnjallday/ori-agent/internal/folderdigest"
)

// Every bound of the walk, the map and the facts. They are the only numbers
// on this path (PRD FR12, FR14, FR15).
const (
	// WalkMaxEntries is how many directory entries one walk lists before it
	// stops and records the folders it has not opened.
	WalkMaxEntries = 2000
	// WalkBudget is the wall-clock budget of one walk.
	WalkBudget = 20 * time.Second
	// LargeFolderEntries is the largest listing the walk opens; a folder
	// holding more is recorded as large and not opened.
	LargeFolderEntries = 5000
	// MaxFolders bounds the folder map. A walk that would record more stops.
	MaxFolders = 20000
	// LookMaxEntries is how many entries one targeted look lists in a single
	// folder before it marks the listing as having more.
	LookMaxEntries = 500

	// PortfolioMinProjects is how many projects of one shape with a Home
	// provider make a portfolio (the digest's own threshold).
	PortfolioMinProjects = folderdigest.PortfolioMinProjects
	// GroupMinMembers is how many projects below the fed folder make the
	// workspace a group (the digest's collection threshold).
	GroupMinMembers = folderdigest.CollectionMinProjects
	// DumpMinLooseFiles and DumpMinKinds are the digest's dump thresholds,
	// applied to the files sitting directly in the fed folder.
	DumpMinLooseFiles = folderdigest.DumpMinLooseFiles
	DumpMinKinds      = folderdigest.DumpMinKinds

	// MapVersion is the schema version of a persisted Map.
	MapVersion = 1
)

// Reasons a walk stopped before it opened every folder it found.
const (
	StopEntries = "entries"
	StopTime    = "time"
	StopMapFull = "map_full"
)
