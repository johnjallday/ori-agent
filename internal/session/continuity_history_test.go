package session

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/workspacecontinuity"
)

func continuitySessionDB(t *testing.T, name string) *database.DB {
	t.Helper()
	db, err := database.Open(t.Context(), &database.Config{Path: filepath.Join(t.TempDir(), name+".db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func continuityHistorySeed(t *testing.T, db *database.DB, messages int) []Message {
	t.Helper()
	const owner = "owned"
	ctx := t.Context()
	if _, err := db.ExecContext(ctx, `INSERT INTO workspaces(id,name,owner_user_id,created_at,updated_at) VALUES(?,?, 'local', CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`, owner, owner); err != nil {
		t.Fatal(err)
	}
	start := time.Date(2024, 4, 5, 6, 7, 8, 0, time.UTC)
	if _, err := db.ExecContext(ctx, `INSERT INTO sessions(id,title,agent_name,workspace_id,message_count,created_at,updated_at) VALUES ('saved','Retained <script>','same-name',?,?,?,?)`, owner, messages, start, start.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO session_tags(session_id,tag,created_at) VALUES('saved','authored',?)`, start); err != nil {
		t.Fatal(err)
	}
	result := make([]Message, 0, messages)
	for i := 0; i < messages; i++ {
		m := Message{ID: "message-" + fmtIndex(i), SessionID: "saved", Role: RoleUser, Content: "saved private <script> " + fmtIndex(i), Model: "offline-model", CreatedAt: start}
		if _, err := db.ExecContext(ctx, `INSERT INTO messages(id,session_id,role,content,model,tokens_used,created_at) VALUES(?,?,?,?,?,?,?)`, m.ID, m.SessionID, m.Role, m.Content, m.Model, m.TokensUsed, m.CreatedAt); err != nil {
			t.Fatal(err)
		}
		result = append(result, m)
	}
	return result
}

func fmtIndex(i int) string { return fmt.Sprintf("%04d", i) }

func collectSessionRecords(t *testing.T, db *database.DB, owner string) ([]workspacecontinuity.Record, []workspacecontinuity.Record) {
	t.Helper()
	spool, err := workspacecontinuity.NewSpool(t.TempDir(), owner)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = spool.Close() }()
	if err := db.InTransaction(t.Context(), func(tx *sql.Tx) error { return CollectContinuitySessions(t.Context(), tx, owner, spool) }); err != nil {
		t.Fatal(err)
	}
	components, readObject, err := spool.Seal(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var sessions, messages []workspacecontinuity.Record
	for _, component := range components {
		if component.Domain != "sessions" {
			continue
		}
		for _, ref := range component.Chunks {
			stream, err := readObject(t.Context(), ref.Digest)
			if err != nil {
				t.Fatal(err)
			}
			chunk, err := workspacecontinuity.DecodeChunk(stream, ref, owner, "sessions")
			if closeErr := stream.Close(); closeErr != nil {
				t.Fatal(closeErr)
			}
			if err != nil {
				t.Fatal(err)
			}
			switch chunk.Family {
			case "sessions":
				sessions = append(sessions, chunk.Records...)
			case "messages":
				messages = append(messages, chunk.Records...)
			default:
				t.Fatal("unexpected family", chunk.Family)
			}
		}
	}
	return sessions, messages
}

func TestCollectContinuitySessionsPagesAllOwnedEqualTimeHistory(t *testing.T) {
	db := continuitySessionDB(t, "source")
	original := continuityHistorySeed(t, db, 259)
	if _, err := db.ExecContext(t.Context(), `INSERT INTO sessions(id,title,agent_name,workspace_id,message_count,created_at,updated_at) VALUES('global','Unowned','same-name',NULL,0,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), `INSERT INTO workspaces(id,name,owner_user_id,created_at,updated_at) VALUES('neighbor','Neighbor','local',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), `INSERT INTO sessions(id,title,agent_name,workspace_id,message_count,created_at,updated_at) VALUES('foreign','Other workspace','same-name','neighbor',0,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	sessions, messages := collectSessionRecords(t, db, "owned")
	if len(sessions) != 1 || len(messages) != len(original) {
		t.Fatal("history truncated or captured global name", len(sessions), len(messages))
	}
	stored, err := DecodeContinuitySession(sessions[0], "owned")
	if err != nil || stored.AgentName != "same-name" || len(stored.Tags) != 1 || stored.MessageCount != 259 {
		t.Fatal("session metadata lost", stored, err)
	}
	for i, record := range messages {
		value, err := DecodeContinuityMessage(record, "owned")
		if err != nil || value.Message.ID != original[i].ID || value.Message.Content != original[i].Content || value.SourceSequence <= 0 {
			t.Fatal("equal-time message order changed", i, err)
		}
	}
	forged := sessions[0]
	forged.Data = []byte(`{"version":1,"workspace_id":"other"}`)
	if _, err := DecodeContinuitySession(forged, "owned"); err == nil {
		t.Fatal("foreign/partial record accepted")
	}
}

func TestCollectContinuitySessionsPagesEveryOwnerWithoutNameFallback(t *testing.T) {
	db := continuitySessionDB(t, "pages")
	ctx := t.Context()
	if _, err := db.ExecContext(ctx, `INSERT INTO workspaces(id,name,owner_user_id,created_at,updated_at) VALUES('owner','Owner','local',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)
	if err := db.InTransaction(ctx, func(tx *sql.Tx) error {
		for i := 0; i < 259; i++ {
			if _, err := tx.ExecContext(ctx, `INSERT INTO sessions(id,title,agent_name,workspace_id,message_count,created_at,updated_at) VALUES(?,?,?,?,0,?,?)`,
				"session-"+fmtIndex(i), "Private", "shared-name", "owner", at, at); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	sessions, messages := collectSessionRecords(t, db, "owner")
	if len(sessions) != 259 || len(messages) != 0 || sessions[0].ID != "session-0000" || sessions[258].ID != "session-0258" {
		t.Fatal("owner keyset skipped a page", len(sessions), len(messages))
	}
}

func TestCollectContinuitySessionsDoesNotLabelUnknownOwnerEmpty(t *testing.T) {
	db := continuitySessionDB(t, "absent-owner")
	spool, err := workspacecontinuity.NewSpool(t.TempDir(), "missing")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = spool.Close() }()
	if err := CollectContinuitySessions(t.Context(), db, "missing", spool); !errors.Is(err, workspacecontinuity.ErrConflict) {
		t.Fatal("unknown owner reported empty sessions", err)
	}
	if err := CollectContinuityToolHistory(t.Context(), db, "missing", spool); !errors.Is(err, workspacecontinuity.ErrConflict) {
		t.Fatal("unknown owner reported empty tool history", err)
	}
}

// A drifted cached message_count must neither block preparation forever nor be
// copied: the checkpoint records the rows actually read in the same view.
func TestCollectContinuitySessionsCountsRetainedRowsAndHonorsCancellation(t *testing.T) {
	db := continuitySessionDB(t, "source")
	continuityHistorySeed(t, db, 2)
	if _, err := db.ExecContext(t.Context(), `UPDATE sessions SET message_count=1 WHERE id='saved'`); err != nil {
		t.Fatal(err)
	}
	spool, err := workspacecontinuity.NewSpool(t.TempDir(), "owned")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = spool.Close() }()
	if err := db.InTransaction(t.Context(), func(tx *sql.Tx) error { return CollectContinuitySessions(t.Context(), tx, "owned", spool) }); err != nil {
		t.Fatal("drifted counter blocked collection", err)
	}
	components, _, err := spool.Seal(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, component := range components {
		if component.Domain == "sessions" && component.Counts["messages"] != 2 {
			t.Fatal("checkpoint trusted the stale counter instead of the retained rows", component.Counts)
		}
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if err := CollectContinuitySessions(canceled, db, "owned", spool); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled read continued", err)
	}
}
