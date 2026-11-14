package folderagent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/johnjallday/ori-agent/internal/foldermap"
)

// ProjectHolderView names a saved child folder without exposing its path.
type ProjectHolderView struct {
	Index int    `json:"index"`
	Name  string `json:"name"`
	Count int    `json:"count"`
	Kinds string `json:"kinds"`
}

type ProjectHoldersView struct {
	ReportKey string              `json:"report_key"`
	Rows      []ProjectHolderView `json:"rows"`
	More      int                 `json:"more,omitempty"`
}

func holderReportKey(rec *Record) string {
	raw, _ := json.Marshal(rec.Map)
	sum := sha256.Sum256(append([]byte(rec.PolicyBinding+"\x00"), raw...))
	return hex.EncodeToString(sum[:])
}

func projectHoldersView(rec *Record) *ProjectHoldersView {
	if !rec.RootPolicy.IsCatchAll() || completed(rec) {
		return nil
	}
	holders := foldermap.ProjectHolders(rec.Map)
	if len(holders) == 0 {
		return nil
	}
	out := &ProjectHoldersView{ReportKey: holderReportKey(rec)}
	for i, holder := range holders {
		if i >= 3 {
			out.More++
			continue
		}
		var kinds []string
		for _, shape := range holder.Shapes {
			kinds = append(kinds, fmt.Sprintf("%d %s", shape.Count, foldermap.ShapeNoun(shape.Shape, shape.Count)))
		}
		out.Rows = append(out.Rows, ProjectHolderView{Index: holder.Index, Name: holder.Name, Count: holder.Count, Kinds: strings.Join(kinds, ", ")})
	}
	return out
}

// ChooseProjectHolder opens the normal hire card for a recorded project holder.
// It does not walk, create or relink anything. The normal Start pipeline retains
// anchor ownership, duplicate and agent checks. A stale map never names a child
// by a new index, and a directory replaced by a symlink is never followed.
func (s *Service) ChooseProjectHolder(ctx context.Context, userID, workspaceID, reportKey string, index int) (*Chosen, error) {
	rec, err := s.Record(userID, workspaceID)
	if err != nil {
		return nil, err
	}
	if rec.Map == nil || !rec.RootPolicy.IsCatchAll() || completed(rec) || rec.State == StateLooking || rec.State == StateSettingUp || reportKey != holderReportKey(rec) {
		return nil, ErrWrongState
	}
	found := false
	for _, holder := range foldermap.ProjectHolders(rec.Map) {
		if holder.Index == index {
			found = true
			break
		}
	}
	if !found || index < 0 || index >= len(rec.Map.Folders) {
		return nil, &Refusal{Code: "holder_unknown", Status: http.StatusBadRequest, Message: "Choose a project folder from this report."}
	}
	rel, err := foldermap.CleanRel(rec.Map.Folders[index].Path)
	if err != nil || rel == "" || strings.Contains(rel, "/") {
		return nil, ErrWrongState
	}
	root, err := s.deps.Workspaces.AnchorRoot(workspaceID)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(root, filepath.FromSlash(rel))
	info, err := os.Lstat(path) // #nosec G304 G703 -- direct child from validated saved walk; no client path
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, &Refusal{Code: "holder_unavailable", Status: http.StatusConflict, Message: "That project folder is no longer here. Look further to refresh the report."}
	}
	return s.choosePath(ctx, userID, path, ""), nil
}
