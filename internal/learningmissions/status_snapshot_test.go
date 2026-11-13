package learningmissions

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"strings"
	"sync"
	"testing"

	"modernc.org/sqlite"
)

// Interleave a real WAL writer immediately after Status reads the owner row.
// Separate connections exercise the durable snapshot, not a service mutex;
// there are no sleeps, production hooks or retries of a failed assertion.
type statusSnapshotConnector struct {
	path       string
	afterOwner func() error
	once       sync.Once
}

func (c *statusSnapshotConnector) Driver() driver.Driver { return &sqlite.Driver{} }

func (c *statusSnapshotConnector) Connect(context.Context) (driver.Conn, error) {
	conn, err := c.Driver().Open(c.path)
	if err != nil {
		return nil, err
	}
	return &statusSnapshotConn{Conn: conn, connector: c}, nil
}

type statusSnapshotConn struct {
	driver.Conn
	connector *statusSnapshotConnector
}

func (c *statusSnapshotConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	return c.Conn.(driver.ConnBeginTx).BeginTx(ctx, opts)
}

func (c *statusSnapshotConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	rows, err := c.Conn.(driver.QueryerContext).QueryContext(ctx, query, args)
	if err != nil || !strings.Contains(query, "FROM learning_owners") {
		return rows, err
	}
	return &statusSnapshotRows{Rows: rows, connector: c.connector}, nil
}

type statusSnapshotRows struct {
	driver.Rows
	connector *statusSnapshotConnector
}

func (r *statusSnapshotRows) Close() error {
	if err := r.Rows.Close(); err != nil {
		return err
	}
	var err error
	r.connector.once.Do(func() { err = r.connector.afterOwner() })
	return err
}

func TestStatusKeepsOwnerAndInstancesInOneSnapshot(t *testing.T) {
	for _, change := range []string{"reconcile", "clear"} {
		t.Run(change, func(t *testing.T) {
			writer, db, instance := reviewFixture(t)
			changed := false
			connector := &statusSnapshotConnector{path: db.Path(), afterOwner: func() error {
				changed = true
				if change == "reconcile" {
					return writer.Reconcile(t.Context(), "local", "workspace")
				}
				_, err := writer.ClearHistory(t.Context(), "local", MutationRequest{OperationID: "clear", ExpectedRevision: 2})
				return err
			}}
			readerDB := sql.OpenDB(connector)
			readerDB.SetMaxOpenConns(1)
			t.Cleanup(func() { _ = readerDB.Close() })
			reader := NewService(&SQLiteStore{db: readerDB}, writer.gate, writer.source)
			reader.now = writer.now

			before, err := reader.Status(t.Context(), "local")
			if err != nil || !changed {
				t.Fatalf("status did not cross the committed write: changed=%v, err=%v", changed, err)
			}
			if before.Owner.Revision != 2 || before.Owner.Epoch != 1 || len(before.Instances) != 1 ||
				before.Instances[0].Revision != instance.Revision || before.Instances[0].Progress.Result != nil {
				t.Fatalf("status mixed pre-write owner with post-write instances: %+v", before)
			}

			after, err := reader.Status(t.Context(), "local")
			if err != nil || after.Owner.Revision != 3 {
				t.Fatalf("next status did not see the committed write: %+v, %v", after, err)
			}
			if change == "clear" {
				if after.Owner.Epoch != 2 || len(after.Instances) != 0 {
					t.Fatalf("clear lost its epoch or revived learning: %+v", after)
				}
				return
			}
			if len(after.Instances) != 1 || after.Instances[0].Revision != 3 || after.Instances[0].Progress.Result == nil {
				t.Fatalf("reconciled result missing: %+v", after)
			}
			// A consistent snapshot can still become stale after it is read.
			// Keep the write fence, then prove a fresh ready result is reviewable.
			req := ReviewRequest{MutationRequest: MutationRequest{OperationID: "review", ExpectedRevision: before.Owner.Revision},
				InstanceRevision: before.Instances[0].Revision, Action: "prepare"}
			if _, err := reader.ReviewResult(t.Context(), "local", instance.ID, req); !errors.Is(err, ErrConflict) {
				t.Fatalf("stale status bypassed the revision check: %v", err)
			}
			req.ExpectedRevision, req.InstanceRevision = after.Owner.Revision, after.Instances[0].Revision
			if out, err := reader.ReviewResult(t.Context(), "local", instance.ID, req); err != nil || out.Review == nil {
				t.Fatalf("fresh status could not prepare review: %+v, %v", out, err)
			}
		})
	}
}
