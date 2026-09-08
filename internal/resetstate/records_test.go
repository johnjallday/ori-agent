package resetstate

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestRecordsAreBoundedPrivateAndSurviveReacquisition(t *testing.T) {
	root := t.TempDir()
	lease := acquireTestLease(t, root)
	data, err := lease.Read(OperationRecord)
	if err != nil || data != nil {
		t.Fatal("missing is not absent:", err)
	}
	payload := []byte(`{"schema_version":1,"state":"awaiting_restart"}`)
	policy := []byte(`{"schema_version":1,"suppress_legacy_agents":true}`)
	if err := lease.Replace(OperationRecord, payload); err != nil {
		t.Fatal(err)
	}
	if err := lease.Replace(PolicyRecord, policy); err != nil {
		t.Fatal(err)
	}
	if err := lease.Replace(OperationRecord, bytes.Repeat([]byte{'x'}, MaxRecordBytes+1)); !errors.Is(err, ErrRecordLimit) {
		t.Fatal(err)
	}
	for _, name := range []Record{"../escape", "/tmp/escape", "other.json"} {
		if err := lease.Replace(name, payload); !errors.Is(err, ErrUnsafeState) {
			t.Fatal(err)
		}
		if _, err := lease.Read(name); !errors.Is(err, ErrUnsafeState) {
			t.Fatal(err)
		}
	}
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
	reopened := acquireTestLease(t, root)
	for name, want := range map[Record][]byte{OperationRecord: payload, PolicyRecord: policy} {
		got, err := reopened.Read(name)
		if err != nil || !bytes.Equal(got, want) {
			t.Fatal("record did not survive:", name, err)
		}
		info, err := os.Stat(filepath.Join(root, Directory, string(name)))
		if err != nil || !privateEntry(info, false) {
			t.Fatal("record is not private:", err)
		}
	}
	entries, err := os.ReadDir(filepath.Join(root, Directory))
	if err != nil || len(entries) != 3 {
		t.Fatal("unbounded temporary/replay files:", len(entries), err)
	}
}

func TestRecordFailureAfterRenameIsUncertainNotRollback(t *testing.T) {
	root := t.TempDir()
	lease := acquireTestLease(t, root)
	before, after := []byte(`{"revision":1}`), []byte(`{"revision":2}`)
	if err := lease.Replace(OperationRecord, before); err != nil {
		t.Fatal(err)
	}
	lease.syncMetadata = func(*os.Root) error { return errors.New("fixture sync failure") }
	if err := lease.Replace(OperationRecord, after); !errors.Is(err, ErrDurabilityUnknown) {
		t.Fatal("false durability claim:", err)
	}
	data, err := lease.Read(OperationRecord)
	if err != nil || !bytes.Equal(data, after) {
		t.Fatal("post-rename failure was treated as rollback:", err)
	}
	if err := lease.RequireCleanStart(); !errors.Is(err, ErrRecoveryRequired) {
		t.Fatal("uncertain result allowed normal startup:", err)
	}
}

func TestRecordsDistinguishEmptyOversizedAndLinkedEvidence(t *testing.T) {
	root := t.TempDir()
	lease := acquireTestLease(t, root)
	path := filepath.Join(root, Directory, string(OperationRecord))
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	data, err := lease.Read(OperationRecord)
	if err != nil || data == nil || len(data) != 0 {
		t.Fatal("empty corrupt file treated as absent:", err)
	}
	if err := os.WriteFile(path, bytes.Repeat([]byte{'x'}, MaxRecordBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := lease.Read(OperationRecord); !errors.Is(err, ErrRecordLimit) {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(t.TempDir(), "retained")
	if err := os.WriteFile(sentinel, []byte("unchanged"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(sentinel, path); err != nil {
		t.Fatal(err)
	}
	if _, err := lease.Read(OperationRecord); !errors.Is(err, ErrUnsafeState) {
		t.Fatal(err)
	}
	if err := lease.Replace(OperationRecord, []byte(`{}`)); !errors.Is(err, ErrUnsafeState) {
		t.Fatal(err)
	}
	data, err = os.ReadFile(sentinel)
	if err != nil || string(data) != "unchanged" {
		t.Fatal("followed receipt link:", err)
	}
}
