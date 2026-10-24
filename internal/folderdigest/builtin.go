package folderdigest

// BuiltinSoftwareProject is the recipe identity of the software-project setup.
const BuiltinSoftwareProject = "software_project"

// BuiltinInboxFolder is the recipe identity of the inbox-folder setup: a File
// Janitor workspace for a folder that is no project of a kind Ori knows.
const BuiltinInboxFolder = "inbox_folder"

// BuiltinCapability is a host-owned setup with nothing to install: no
// integration, provider or plugin stands behind it, and no reviewed-registry
// entry is invented for it. Recipe is its stable server-side identity and
// Version changes whenever what its Set up does changes. The rows that carry
// one live in tables.go with every other recognition row.
type BuiltinCapability struct {
	Recipe  string
	Version int
	// DisplayName names the kind of project in a sentence ("software project").
	DisplayName string
	// Workspace names the workspace the setup creates.
	Workspace    string
	Question     string
	AcceptLabel  string
	DeclineLabel string
}

// BuiltinCapabilityFor returns the host-owned setup for a project whose marker
// belongs to a row that has one. Shape alone is insufficient: a picked file
// can lend a folder its shape, and only a marker seen directly inside the
// folder says the folder itself is that kind of project.
func BuiltinCapabilityFor(shape Shape, markerName string) (CapabilityRow, bool) {
	row, ok := CapabilityForShape(shape)
	if !ok || row.Builtin == nil || markerName == "" {
		return CapabilityRow{}, false
	}
	for _, marker := range row.Markers {
		if marker.Name == markerName {
			return row, true
		}
	}
	return CapabilityRow{}, false
}

// InboxCapability returns the host-owned setup for an inbox folder. Unlike
// BuiltinCapabilityFor it is keyed by a verdict, never by a marker or a folder
// name: the caller has already decided the root is an inbox (IsInbox).
func InboxCapability() (CapabilityRow, bool) {
	row, ok := CapabilityForShape(ShapeInbox)
	if !ok || row.Builtin == nil || row.Builtin.Recipe != BuiltinInboxFolder {
		return CapabilityRow{}, false
	}
	return row, true
}
