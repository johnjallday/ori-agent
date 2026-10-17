//go:build darwin || linux

package backuprecoveryhttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/backuprecovery"
	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/pathselection"
	"github.com/johnjallday/ori-agent/internal/resetstate"
	"github.com/johnjallday/ori-agent/internal/vault"
)

// The real HTTP/domain/persistence path with an unavailable adapter. Native UI
// and chosen-reference metadata are invented controls; no source/drive is read.
type chosenFolderFixture struct{}

func (chosenFolderFixture) Available() bool { return true }
func (chosenFolderFixture) Choose(_ context.Context, prompt string) (string, bool, error) {
	switch {
	case strings.Contains(prompt, "source folder"):
		return "/Invented/Project", true, nil
	case strings.Contains(prompt, "volume root"):
		return "/Volumes/Invented", true, nil
	default:
		return "/Volumes/Invented/Repository", true, nil
	}
}

type selectedCredentialFixture struct{ uses atomic.Int32 }

func (c *selectedCredentialFixture) DescribeBackupSecret(ctx context.Context, ref vault.BackupSecretReference, authorize vault.BackupSecretAuthorization) (vault.BackupSecretReference, error) {
	if authorize == nil || authorize(ctx) != nil {
		return vault.BackupSecretReference{}, vault.ErrBackupSecretUnavailable
	}
	ref.Revision = strings.Repeat("a", 64)
	return ref, nil
}
func (c *selectedCredentialFixture) UseBackupSecret(context.Context, vault.BackupSecretReference, vault.BackupSecretAuthorization, func([]byte) error) error {
	c.uses.Add(1)
	return vault.ErrBackupSecretUnavailable
}

func TestBackupHTTPRealDomainUnavailablePathNeverGrantsOrDecrypts(t *testing.T) {
	db, err := database.Open(t.Context(), &database.Config{InMemory: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	_, err = db.ExecContext(t.Context(), `INSERT INTO workspaces(id,name,created_at,updated_at,installed_capabilities_json) VALUES('invented-backup','Invented',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,'[{"id":"backup-recovery","version":1,"installed_at":"2026-01-01T00:00:00Z"}]'); INSERT INTO continuity_attachments(workspace_id,state,disposition,updated_at) VALUES('invented-backup','native','ordinary',CURRENT_TIMESTAMP)`)
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(t.TempDir(), "data")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	credentials := &selectedCredentialFixture{}
	gate := &resetstate.WorkGate{}
	service, err := backuprecovery.NewService(backuprecovery.ServiceOptions{Database: db, Secrets: vault.NewMemorySecretStore(), DataDir: root, WorkGate: gate, Credentials: credentials, Selections: pathselection.NewStore(), Picker: chosenFolderFixture{}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := service.Close(ctx); err != nil {
			t.Error(err)
		}
	}()
	mux := backupMux(service)
	call := func(method, path string, payload any) map[string]json.RawMessage {
		t.Helper()
		body := ""
		if payload != nil {
			encoded, err := json.Marshal(payload)
			if err != nil {
				t.Fatal(err)
			}
			body = string(encoded)
		}
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, backupRequest(method, path, body))
		if response.Code != http.StatusOK {
			t.Fatal(path, response.Code, response.Body.String())
		}
		var result map[string]json.RawMessage
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	pick := func(kind string) string {
		t.Helper()
		result := call(http.MethodPost, "/selection", map[string]string{"kind": kind})
		var selection struct {
			Token string `json:"token"`
		}
		if err := json.Unmarshal(result["selection"], &selection); err != nil || selection.Token == "" {
			t.Fatal("missing scoped selection", err)
		}
		return selection.Token
	}
	credentialResult := call(http.MethodPost, "/credential", map[string]string{"vault_id": "invented-vault", "record_id": "invented-record"})
	var credential backuprecovery.CredentialReference
	if err := json.Unmarshal(credentialResult["credential"], &credential); err != nil {
		t.Fatal(err)
	}
	reviewResult := call(http.MethodPost, "/review", backuprecovery.ReviewRequest{SourceSelections: []string{pick("source")}, VolumeSelection: pick("volume"), RepositorySelection: pick("repository"), Credential: credential})
	var review backuprecovery.Review
	if err := json.Unmarshal(reviewResult["review"], &review); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM backup_recovery_local`).Scan(&count); err != nil || count != 0 {
		t.Fatal("selection/review created local state", count, err)
	}
	request := backuprecovery.ConnectRequest{Generation: review.Generation, Revision: review.Revision, ReviewToken: review.Token, OperationID: uuid.NewString(), AllowMetadataRead: true}
	first := call(http.MethodPost, "/connect", request)
	retry := call(http.MethodPost, "/connect", request)
	if string(first["operation"]) != string(retry["operation"]) {
		t.Fatal("lost-response retry changed outcome")
	}
	var operation backuprecovery.OperationResult
	if err := json.Unmarshal(first["operation"], &operation); err != nil || operation.Outcome != "provider_unavailable" {
		t.Fatal("missing tool became a successful check", operation, err)
	}
	statusResult := call(http.MethodGet, "", nil)
	var status backuprecovery.Status
	if err := json.Unmarshal(statusResult["status"], &status); err != nil || status.Connection != "review_required" || status.RuntimeAvailable || status.LastSuccessfulRead != nil || status.SnapshotCount != nil {
		t.Fatal("inert candidate claimed connected evidence", status, err)
	}
	if err := db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM backup_recovery_receipts`).Scan(&count); err != nil || count != 0 || credentials.uses.Load() != 0 {
		t.Fatal("unavailable path created authority or decrypted a password", count, credentials.uses.Load(), err)
	}
	call(http.MethodDelete, "", backuprecovery.ControlRequest{Generation: status.Generation, Revision: status.Revision, OperationID: uuid.NewString()})
	settingsResult := call(http.MethodGet, "/settings", nil)
	var settings backuprecovery.Settings
	if err := json.Unmarshal(settingsResult["settings"], &settings); err != nil || settings.Configured || len(settings.SourcePaths) != 0 || settings.Credential != nil {
		t.Fatal("remove retained an active private selection", settings, err)
	}
	historyResult := call(http.MethodGet, "/history", nil)
	var history backuprecovery.History
	if err := json.Unmarshal(historyResult["history"], &history); err != nil || len(history.Items) != 1 || history.Items[0].Outcome != "provider_unavailable" {
		t.Fatal("remove erased labelled local history", history, err)
	}
}
