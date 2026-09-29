package session

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"time"
	"unicode/utf8"

	"github.com/johnjallday/ori-agent/internal/workspacecontinuity"
)

// ContinuitySession stores historical workspace membership and tags, not a
// provider session, agent permission, or executable message. An agent name is
// a label, never proof of a workspace-local agent instance.
type ContinuitySession struct {
	Version      int                    `json:"version"`
	WorkspaceID  string                 `json:"workspace_id"`
	ID           string                 `json:"id"`
	Title        string                 `json:"title"`
	AgentName    string                 `json:"agent_name"`
	MessageCount int                    `json:"message_count"`
	CreatedAt    time.Time              `json:"created_at"`
	UpdatedAt    time.Time              `json:"updated_at"`
	Tags         []ContinuitySessionTag `json:"tags"`
}

type ContinuitySessionTag struct {
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}

// SourceSequence is the source SQLite message rowid. It preserves ties among
// equal timestamps without assuming that imported rows can reuse source rowids.
type ContinuityMessage struct {
	Version        int     `json:"version"`
	WorkspaceID    string  `json:"workspace_id"`
	SourceSequence int64   `json:"source_sequence"`
	Message        Message `json:"message"`
}

func SnapshotContinuitySession(value ContinuitySession, owner string) (workspacecontinuity.Record, error) {
	if value.Version != 1 || value.WorkspaceID != owner || !workspacecontinuity.ValidID(owner) || !workspacecontinuity.ValidID(value.ID) ||
		!utf8.ValidString(value.Title) || !utf8.ValidString(value.AgentName) || value.CreatedAt.IsZero() || value.UpdatedAt.IsZero() ||
		value.MessageCount < 0 || len(value.Tags) > workspacecontinuity.MaxFiles || value.Tags == nil {
		return workspacecontinuity.Record{}, workspacecontinuity.ErrInvalid
	}
	seen := map[string]bool{}
	for _, tag := range value.Tags {
		if tag.Name == "" || len(tag.Name) > 1024 || !utf8.ValidString(tag.Name) || tag.CreatedAt.IsZero() || seen[tag.Name] {
			return workspacecontinuity.Record{}, workspacecontinuity.ErrInvalid
		}
		seen[tag.Name] = true
	}
	return workspacecontinuity.EncodeRecord(value.ID, value)
}

func DecodeContinuitySession(record workspacecontinuity.Record, owner string) (ContinuitySession, error) {
	var value ContinuitySession
	if err := workspacecontinuity.DecodeRecord(record, &value); err != nil {
		return value, err
	}
	if value.Version != 1 {
		return value, workspacecontinuity.ErrVersion
	}
	encoded, err := SnapshotContinuitySession(value, owner)
	if err != nil {
		return value, err
	}
	if record.ID != encoded.ID || !bytes.Equal(record.Data, encoded.Data) {
		return value, workspacecontinuity.ErrInvalid
	}
	return value, nil
}

func SnapshotContinuityMessage(value ContinuityMessage, owner string) (workspacecontinuity.Record, error) {
	m := value.Message
	if value.Version != 1 || value.WorkspaceID != owner || !workspacecontinuity.ValidID(owner) || !workspacecontinuity.ValidID(m.ID) ||
		!workspacecontinuity.ValidID(m.SessionID) || value.SourceSequence <= 0 || m.CreatedAt.IsZero() || m.TokensUsed < 0 ||
		!utf8.ValidString(m.Content) || !utf8.ValidString(m.Model) ||
		m.Role != RoleUser && m.Role != RoleAssistant && m.Role != RoleSystem {
		return workspacecontinuity.Record{}, workspacecontinuity.ErrInvalid
	}
	return workspacecontinuity.EncodeRecord(m.ID, value)
}

func DecodeContinuityMessage(record workspacecontinuity.Record, owner string) (ContinuityMessage, error) {
	var value ContinuityMessage
	if err := workspacecontinuity.DecodeRecord(record, &value); err != nil {
		return value, err
	}
	if value.Version != 1 {
		return value, workspacecontinuity.ErrVersion
	}
	encoded, err := SnapshotContinuityMessage(value, owner)
	if err != nil {
		return value, err
	}
	if record.ID != encoded.ID || !bytes.Equal(record.Data, encoded.Data) {
		return value, workspacecontinuity.ErrInvalid
	}
	return value, nil
}

// CollectContinuitySessions reads every explicitly workspace-owned session and
// its messages through one caller-owned SQL snapshot. Global/name-only history
// is deliberately excluded. Rows are keyset paged and written into bounded
// spool chunks; the caller discards the spool on ANY error or stale view.
func CollectContinuitySessions(ctx context.Context, query workspacecontinuity.Queryer, owner string, spool *workspacecontinuity.Spool) error {
	if query == nil || spool == nil || !workspacecontinuity.ValidID(owner) {
		return workspacecontinuity.ErrInvalid
	}
	if err := requireContinuitySourceOwner(ctx, query, owner); err != nil {
		return err
	}
	const pageSize = 256
	after, sessions := "", 0
	for {
		rows, err := query.QueryContext(ctx, `SELECT id,title,agent_name,message_count,created_at,updated_at FROM sessions WHERE workspace_id=? AND id>? ORDER BY id LIMIT ?`, owner, after, pageSize)
		if err != nil {
			return err
		}
		page := make([]ContinuitySession, 0, pageSize)
		for rows.Next() {
			var item ContinuitySession
			if err := rows.Scan(&item.ID, &item.Title, &item.AgentName, &item.MessageCount, &item.CreatedAt, &item.UpdatedAt); err != nil {
				_ = rows.Close()
				return err
			}
			item.Version, item.WorkspaceID, item.Tags = 1, owner, []ContinuitySessionTag{}
			page = append(page, item)
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return err
		}
		for _, item := range page {
			if err := ctx.Err(); err != nil {
				return err
			}
			if sessions >= workspacecontinuity.MaxReferences*workspacecontinuity.MaxRecords {
				return workspacecontinuity.ErrLimit
			}
			tags, err := collectContinuitySessionTags(ctx, query, item.ID)
			if err != nil {
				return err
			}
			item.Tags = tags
			// The cached sessions.message_count can drift from the rows (older
			// builds, maintenance deletes). The checkpoint records the retained
			// rows actually read in this same view, never the stale counter.
			if err := query.QueryRowContext(ctx, `SELECT COUNT(*) FROM messages WHERE session_id=?`, item.ID).Scan(&item.MessageCount); err != nil {
				return err
			}
			record, err := SnapshotContinuitySession(item, owner)
			if err != nil {
				return err
			}
			if err := spool.AddRecord(ctx, "sessions", "sessions", record); err != nil {
				return err
			}
			count, err := collectContinuityMessages(ctx, query, owner, item.ID, spool)
			if err != nil {
				return err
			}
			if count != item.MessageCount {
				return workspacecontinuity.ErrChanged
			}
			after = item.ID
			sessions++
		}
		if len(page) < pageSize {
			break
		}
	}
	if sessions == 0 {
		return spool.SetAvailability(ctx, "sessions", workspacecontinuity.Empty, "")
	}
	return nil
}

func requireContinuitySourceOwner(ctx context.Context, query workspacecontinuity.Queryer, owner string) error {
	var exists int
	if err := query.QueryRowContext(ctx, `SELECT COUNT(*) FROM workspaces WHERE id=? AND owner_user_id='local' AND deleted_at IS NULL`, owner).Scan(&exists); err != nil {
		return err
	}
	if exists != 1 {
		return workspacecontinuity.ErrConflict
	}
	return nil
}

func collectContinuitySessionTags(ctx context.Context, query workspacecontinuity.Queryer, sessionID string) ([]ContinuitySessionTag, error) {
	rows, err := query.QueryContext(ctx, `SELECT tag,created_at FROM session_tags WHERE session_id=? ORDER BY tag LIMIT ?`, sessionID, workspacecontinuity.MaxFiles+1)
	if err != nil {
		return nil, err
	}
	result := make([]ContinuitySessionTag, 0)
	for rows.Next() {
		if len(result) >= workspacecontinuity.MaxFiles {
			_ = rows.Close()
			return nil, workspacecontinuity.ErrLimit
		}
		var tag ContinuitySessionTag
		if err := rows.Scan(&tag.Name, &tag.CreatedAt); err != nil {
			_ = rows.Close()
			return nil, err
		}
		result = append(result, tag)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, err
	}
	return result, nil
}

func collectContinuityMessages(ctx context.Context, query workspacecontinuity.Queryer, owner, sessionID string, spool *workspacecontinuity.Spool) (int, error) {
	const pageSize = 256
	var after int64
	count := 0
	for {
		rows, err := query.QueryContext(ctx, `SELECT rowid,id,session_id,role,content,model,tokens_used,created_at FROM messages WHERE session_id=? AND rowid>? ORDER BY rowid LIMIT ?`, sessionID, after, pageSize)
		if err != nil {
			return count, err
		}
		page := 0
		for rows.Next() {
			if err := ctx.Err(); err != nil {
				_ = rows.Close()
				return count, err
			}
			if count >= workspacecontinuity.MaxReferences*workspacecontinuity.MaxRecords {
				_ = rows.Close()
				return count, workspacecontinuity.ErrLimit
			}
			var value ContinuityMessage
			var model sql.NullString
			if err := rows.Scan(&value.SourceSequence, &value.Message.ID, &value.Message.SessionID, &value.Message.Role, &value.Message.Content, &model, &value.Message.TokensUsed, &value.Message.CreatedAt); err != nil {
				_ = rows.Close()
				return count, err
			}
			value.Version, value.WorkspaceID, value.Message.Model = 1, owner, model.String
			if value.Message.SessionID != sessionID {
				_ = rows.Close()
				return count, workspacecontinuity.ErrInvalid
			}
			record, err := SnapshotContinuityMessage(value, owner)
			if err != nil {
				_ = rows.Close()
				return count, err
			}
			if err := spool.AddRecord(ctx, "sessions", "messages", record); err != nil {
				_ = rows.Close()
				return count, err
			}
			after = value.SourceSequence
			count++
			page++
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return count, err
		}
		if page < pageSize {
			break
		}
	}
	return count, nil
}
