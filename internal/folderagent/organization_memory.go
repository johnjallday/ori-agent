package folderagent

import (
	"github.com/google/uuid"
	"strings"

	"github.com/johnjallday/ori-agent/internal/sensitive"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// The feedback line is canonical. Resolve it on every visit (and send), never
// import a stale sidecar copy after the Memory tab has edited or removed it.
type ReviewMemory interface {
	SnapshotExact(string) (workspace.MemoryExactSnapshot, error)
	EditExact(string, workspace.MemoryExactTarget, *workspace.MemoryEntry) error
	AppendExact(string, string, workspace.MemoryEntry) (workspace.MemoryExactTarget, error)
}
type DocumentsConvention struct {
	Proof    string `json:"-"`
	Text     string `json:"text,omitempty"`
	Revision string `json:"revision"`
	Conflict bool   `json:"conflict,omitempty"`
}

func conventionMarker(owner, id, rootWitness string) string {
	return "documents-organizing-" + documentsRef(owner, id, rootWitness)[:24]
}
func (s *Service) convention(id, marker string) (DocumentsConvention, workspace.MemoryExactTarget, error) {
	var c DocumentsConvention
	var target workspace.MemoryExactTarget
	if s.deps.OrganizationMemory == nil {
		return c, target, nil
	}
	snap, err := s.deps.OrganizationMemory.SnapshotExact(id)
	if err != nil {
		return c, target, err
	}
	c.Revision = snap.FileHash
	for _, e := range snap.Entries {
		if e.Entry.Provenance != marker && !strings.HasPrefix(e.Entry.Provenance, marker+"/") {
			continue
		}
		if target.LineHash != "" || e.Entry.Type != workspace.MemoryTypeFeedback || !conventionTextOK(e.Entry.Text) {
			c.Conflict = true
			c.Text = ""
			break
		}
		c.Text = e.Entry.Text
		target = e.Target
		c.Proof = documentsRef(target.Provenance, target.LineHash)
	}
	return c, target, nil
}
func conventionTextOK(s string) bool {
	return s != "" && len(s) <= 320 && !strings.ContainsAny(s, "\x00\r\n") && !sensitive.ContainsSecretLikeText(s)
}
func (s *Service) SaveConvention(owner, id, revision, text string) (DocumentsConvention, error) {
	rec, _, err := s.latestReview(owner, id)
	if err != nil {
		return DocumentsConvention{}, err
	}
	root, _ := rec.Map.Folder("")
	marker := conventionMarker(owner, id, root.BranchWitness)
	c, target, err := s.convention(id, marker)
	if err != nil || revision == "" || c.Revision != revision || c.Conflict {
		return DocumentsConvention{}, ErrWrongState
	}
	text = strings.TrimSpace(text)
	if text != "" && !conventionTextOK(text) {
		return DocumentsConvention{}, ErrWrongState
	}
	entry := workspace.MemoryEntry{Type: workspace.MemoryTypeFeedback, Date: s.deps.Now().UTC().Format("2006-01-02"), Text: text, Provenance: marker}
	switch {
	case target.LineHash != "" && text == "":
		err = s.deps.OrganizationMemory.EditExact(id, target, nil)
	case target.LineHash != "":
		entry.Provenance = target.Provenance
		err = s.deps.OrganizationMemory.EditExact(id, target, &entry)
	case text != "":
		entry.Provenance = marker + "/" + uuid.NewString()
		_, err = s.deps.OrganizationMemory.AppendExact(id, c.Revision, entry)
	default:
		return DocumentsConvention{}, ErrWrongState
	}
	if err != nil {
		return DocumentsConvention{}, ErrWrongState
	}
	if _, _, err = s.latestReview(owner, id); err != nil {
		return DocumentsConvention{}, err
	}
	c, _, err = s.convention(id, marker)
	return c, err
}
