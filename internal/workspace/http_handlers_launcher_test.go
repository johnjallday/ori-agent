package workspace

import (
	"errors"
	"net/http"
	"os"
	"testing"
)

func TestFolderPickerLaunchErrorResponse_NotFound(t *testing.T) {
	status, message := folderPickerLaunchErrorResponse(errors.Join(errFolderPickerAppNotFound, os.ErrNotExist))
	if status != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", status)
	}
	if message == "" {
		t.Fatal("expected not-found message")
	}
}

func TestFolderPickerLaunchErrorResponse_GenericFailure(t *testing.T) {
	status, message := folderPickerLaunchErrorResponse(errors.New("launch failed"))
	if status != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", status)
	}
	if message != "Failed to launch folder picker: launch failed" {
		t.Fatalf("unexpected message: %q", message)
	}
}
