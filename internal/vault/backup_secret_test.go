package vault

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func backupSecretFixture(t *testing.T) (*Store, BackupSecretReference) {
	t.Helper()
	s, db := newTestVaultStore(t)
	t.Cleanup(func() { _ = db.Close() })
	// Fixed timestamps deliberately demonstrate that revision is not a clock.
	s.now = func() time.Time { return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC) }
	v := createTestVault(t, t.Context(), s, "Invented backup test vault")
	r := &Record{VaultID: v.ID, Type: "secret", WorkspaceID: "workspace-one", Label: "Invented credential",
		Payload: json.RawMessage(`{"value":"  invented password  "}`)}
	if err := s.CreateRecord(t.Context(), r, AccessContext{}); err != nil {
		t.Fatal(err)
	}
	ref, err := s.DescribeBackupSecret(t.Context(), BackupSecretReference{
		VaultID: v.ID, RecordID: r.ID, WorkspaceID: r.WorkspaceID,
	}, allowBackupSecret)
	if err != nil {
		t.Fatal(err)
	}
	return s, ref
}

func allowBackupSecret(context.Context) error { return nil }

func TestBackupSecretScopedTransferPreservesWhitespaceAndClearsBuffer(t *testing.T) {
	s, ref := backupSecretFixture(t)
	var retained []byte
	calls := 0
	err := s.UseBackupSecret(t.Context(), ref, func(context.Context) error { calls++; return nil }, func(value []byte) error {
		if string(value) != "  invented password  " {
			t.Error("credential whitespace changed")
		}
		retained = value // test-only: verify the documented owned-slice cleanup
		return nil
	})
	if err != nil || calls != 3 || len(retained) == 0 {
		t.Fatalf("transfer failed: err=%v authorization_checks=%d", err, calls)
	}
	for _, b := range retained {
		if b != 0 {
			t.Fatal("credential transfer buffer was not cleared")
		}
	}
	encoded, err := json.Marshal(ref)
	if err != nil || string(encoded) != "{}" {
		t.Fatal("host-only secret reference entered JSON projection")
	}
}

func TestBackupSecretRejectsWrongScopeAndRevision(t *testing.T) {
	s, ref := backupSecretFixture(t)
	for name, change := range map[string]func(*BackupSecretReference){
		"wrong_vault":            func(r *BackupSecretReference) { r.VaultID = "different-vault" },
		"wrong_workspace":        func(r *BackupSecretReference) { r.WorkspaceID = "workspace-two" },
		"case_changed_workspace": func(r *BackupSecretReference) { r.WorkspaceID = "WORKSPACE-ONE" },
		"wrong_record":           func(r *BackupSecretReference) { r.RecordID = "different-record" },
		"missing_revision":       func(r *BackupSecretReference) { r.Revision = "" },
		"wrong_revision":         func(r *BackupSecretReference) { r.Revision = strings.Repeat("0", 64) },
		"path_not_id":            func(r *BackupSecretReference) { r.VaultID = "../different-vault" },
		"oversized_id":           func(r *BackupSecretReference) { r.RecordID = strings.Repeat("a", 129) },
	} {
		t.Run(name, func(t *testing.T) {
			changed := ref
			change(&changed)
			err := s.UseBackupSecret(t.Context(), changed, allowBackupSecret, func([]byte) error {
				t.Error("secret delivered for wrong scope or revision")
				return nil
			})
			if !errors.Is(err, ErrBackupSecretUnavailable) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestBackupSecretRejectsAbsentRevokedAndCancelledAuthority(t *testing.T) {
	s, ref := backupSecretFixture(t)
	for _, failAt := range []int{1, 2, 3} {
		calls := 0
		err := s.UseBackupSecret(t.Context(), ref, func(context.Context) error {
			calls++
			if calls == failAt {
				return errors.New("private authorization detail must not escape")
			}
			return nil
		}, func([]byte) error { t.Error("revoked secret delivered"); return nil })
		if !errors.Is(err, ErrBackupSecretUnavailable) || calls != failAt {
			t.Fatalf("failed authority check %d: %v", failAt, err)
		}
	}
	if err := s.UseBackupSecret(t.Context(), ref, nil, func([]byte) error { t.Error("nil authority accepted"); return nil }); !errors.Is(err, ErrBackupSecretUnavailable) {
		t.Fatal("missing authority accepted")
	}
	if _, err := s.DescribeBackupSecret(t.Context(), ref, nil); !errors.Is(err, ErrBackupSecretUnavailable) {
		t.Fatal("missing description authority accepted")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := s.UseBackupSecret(ctx, ref, allowBackupSecret, func([]byte) error { t.Error("cancelled use"); return nil }); !errors.Is(err, ErrBackupSecretUnavailable) {
		t.Fatal("cancelled context accepted")
	}
}

func TestBackupSecretDescribeDoesNotDecryptOrSearchOtherVaults(t *testing.T) {
	s, ref := backupSecretFixture(t)
	other := createTestVault(t, t.Context(), s, "Unrelated invented vault")
	if _, err := s.db.ExecContext(t.Context(), `UPDATE vaults SET file_path='does-not-exist' WHERE id=?`, other.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.Lock(t.Context(), ref.VaultID); err != nil {
		t.Fatal(err)
	}
	got, err := s.DescribeBackupSecret(t.Context(), ref, allowBackupSecret)
	if err != nil || got.Revision != ref.Revision {
		t.Fatalf("metadata-only scoped description failed: %v", err)
	}
	if err := s.UseBackupSecret(t.Context(), ref, allowBackupSecret, func([]byte) error { t.Error("locked secret delivered"); return nil }); !errors.Is(err, ErrBackupSecretUnavailable) {
		t.Fatal("locked vault accepted")
	}
}

func TestBackupSecretRejectsWrongTypeAndMovedRecord(t *testing.T) {
	for _, recordType := range []string{"personal_note", "email_account", "credential", "secret"} {
		t.Run(recordType, func(t *testing.T) {
			s, ref := backupSecretFixture(t)
			workspace := "workspace-one"
			if recordType == "secret" {
				workspace = "workspace-two"
			}
			if _, err := s.UpdateRecord(t.Context(), ref.RecordID, RecordUpdate{Type: &recordType, WorkspaceID: &workspace}, AccessContext{}); err != nil {
				t.Fatal(err)
			}
			if _, err := s.DescribeBackupSecret(t.Context(), ref, allowBackupSecret); !errors.Is(err, ErrBackupSecretUnavailable) {
				t.Fatal("wrong type/scope described")
			}
			if err := s.UseBackupSecret(t.Context(), ref, allowBackupSecret, func([]byte) error { t.Error("wrong record delivered"); return nil }); !errors.Is(err, ErrBackupSecretUnavailable) {
				t.Fatal("wrong type/scope accepted")
			}
		})
	}
}

func TestBackupSecretChangedPayloadAtSameTimestampInvalidatesReference(t *testing.T) {
	s, ref := backupSecretFixture(t)
	payload := json.RawMessage(`{"value":"changed invented password"}`)
	if _, err := s.UpdateRecord(t.Context(), ref.RecordID, RecordUpdate{Payload: &payload}, AccessContext{}); err != nil {
		t.Fatal(err)
	}
	if err := s.UseBackupSecret(t.Context(), ref, allowBackupSecret, func([]byte) error { t.Error("stale reference delivered"); return nil }); !errors.Is(err, ErrBackupSecretUnavailable) {
		t.Fatal("stale reference accepted")
	}
	updated, err := s.DescribeBackupSecret(t.Context(), ref, allowBackupSecret)
	if err != nil || updated.Revision == ref.Revision {
		t.Fatal("ciphertext change did not invalidate revision")
	}
}

func TestBackupSecretRechecksChangesAndLockBeforeTransfer(t *testing.T) {
	for _, action := range []string{"change", "lock", "cancel"} {
		t.Run(action, func(t *testing.T) {
			s, ref := backupSecretFixture(t)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			calls := 0
			err := s.UseBackupSecret(ctx, ref, func(context.Context) error {
				calls++
				if action == "change" && calls == 2 {
					payload := json.RawMessage(`{"value":"new invented value"}`)
					_, err := s.UpdateRecord(ctx, ref.RecordID, RecordUpdate{Payload: &payload}, AccessContext{})
					return err
				}
				if calls == 3 {
					if action == "lock" {
						return s.Lock(ctx, ref.VaultID)
					}
					if action == "cancel" {
						cancel()
					}
				}
				return nil
			}, func([]byte) error { t.Error("changed/locked/cancelled credential delivered"); return nil })
			if err != ErrBackupSecretUnavailable {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestBackupSecretErrorsDoNotExposeCallbackOrProviderDiagnostics(t *testing.T) {
	s, ref := backupSecretFixture(t)
	err := s.UseBackupSecret(t.Context(), ref, allowBackupSecret, func([]byte) error {
		return errors.New("invented password /private/invented/path provider diagnostic")
	})
	if err != ErrBackupSecretUnavailable {
		t.Fatalf("non-fixed error returned: %v", err)
	}
}

func TestBackupSecretRejectsMalformedPayload(t *testing.T) {
	for name, payload := range map[string]string{
		"empty": ``, "array": `[]`, "null": `null`, "missing": `{}`,
		"wrong_key": `{"password":"invented"}`, "wrong_case": `{"Value":"invented"}`,
		"number": `{"value":123}`, "empty_password": `{"value":""}`,
		"duplicate":   `{"value":"one","value":"two"}`,
		"extra":       `{"value":"one","extra":"two"}`,
		"attachments": `{"value":"one","attachments":[]}`,
		"trailing":    `{"value":"one"} {}`, "truncated": `{"value":"one"`,
		"newline": `{"value":"one\ntwo"}`, "carriage_return": `{"value":"one\rtwo"}`,
		"nul": `{"value":"one\u0000two"}`, "surrogate": `{"value":"\ud800"}`,
		"invalid_utf8":   "{\"value\":\"\xff\"}",
		"too_long":       `{"value":"` + strings.Repeat("x", maxBackupPasswordBytes+1) + `"}`,
		"too_large_json": strings.Repeat(" ", maxBackupSecretPayload) + `{}`,
	} {
		t.Run(name, func(t *testing.T) {
			if value, err := decodeBackupPassword([]byte(payload)); len(value) != 0 || err != ErrBackupSecretUnavailable {
				t.Fatal("malformed payload accepted")
			}
		})
	}
	payload := `{"value":"` + strings.Repeat("x", maxBackupPasswordBytes) + `"}`
	if value, err := decodeBackupPassword([]byte(payload)); err != nil || len(value) != maxBackupPasswordBytes {
		t.Fatal("maximum password rejected")
	}
}
