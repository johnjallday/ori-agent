package http

import (
	"errors"
	"net"
	stdhttp "net/http"
	"net/http/httptest"
	"os"
	"syscall"
	"testing"
)

type failingResponseWriter struct {
	header   stdhttp.Header
	writeErr error
}

func (w *failingResponseWriter) Header() stdhttp.Header {
	if w.header == nil {
		w.header = make(stdhttp.Header)
	}
	return w.header
}

func (w *failingResponseWriter) WriteHeader(_ int) {}

func (w *failingResponseWriter) Write(_ []byte) (int, error) {
	if w.writeErr != nil {
		return 0, w.writeErr
	}
	return 1, nil
}

func TestIsClientDisconnectError(t *testing.T) {
	disconnectErr := &net.OpError{
		Op:  "write",
		Net: "tcp",
		Err: &os.SyscallError{Syscall: "write", Err: syscall.EPIPE},
	}
	if !IsClientDisconnectError(disconnectErr) {
		t.Fatalf("expected disconnect error to be detected")
	}

	if !IsClientDisconnectError(errors.New("write tcp 127.0.0.1:8765->127.0.0.1:59452: write: broken pipe")) {
		t.Fatalf("expected broken pipe text error to be detected")
	}

	if IsClientDisconnectError(errors.New("json: unsupported type: func()")) {
		t.Fatalf("unexpected disconnect classification for normal encoding error")
	}
}

func TestRespondJSON_IgnoresClientDisconnect(t *testing.T) {
	w := &failingResponseWriter{
		writeErr: &net.OpError{
			Op:  "write",
			Net: "tcp",
			Err: &os.SyscallError{Syscall: "write", Err: syscall.EPIPE},
		},
	}

	if err := RespondJSON(w, stdhttp.StatusOK, map[string]string{"ok": "true"}); err != nil {
		t.Fatalf("expected nil for client disconnect, got %v", err)
	}
}

func TestRespondJSON_ReturnsRealError(t *testing.T) {
	w := &failingResponseWriter{writeErr: errors.New("disk full")}
	if err := RespondJSON(w, stdhttp.StatusOK, map[string]string{"ok": "true"}); err == nil {
		t.Fatalf("expected non-nil error")
	}
}

func TestRespondAPIError_IgnoresClientDisconnect(t *testing.T) {
	w := &failingResponseWriter{
		writeErr: &net.OpError{
			Op:  "write",
			Net: "tcp",
			Err: &os.SyscallError{Syscall: "write", Err: syscall.ECONNRESET},
		},
	}

	err := RespondAPIError(w, stdhttp.StatusBadRequest, NewAPIError(ErrCodeBadRequest, "bad request"))
	if err != nil {
		t.Fatalf("expected nil for client disconnect, got %v", err)
	}
}

func TestWriteJSON_StillWritesNormally(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteJSON(rec, map[string]string{"status": "ok"})
	if rec.Code != stdhttp.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}
}
