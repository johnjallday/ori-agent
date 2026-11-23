package fileparser

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ledongthuc/pdf"
)

const ReviewFileBytes = 1 << 20
const ReviewExcerptBytes = 1536
const reviewPDFMode = "--ori-private-review-pdf"

type ReviewExcerpt struct {
	Text      string
	Truncated bool
}

func trimReviewText(s string) ReviewExcerpt {
	s = strings.TrimSpace(s)
	e := ReviewExcerpt{Text: s}
	if len(s) > ReviewExcerptBytes {
		n := ReviewExcerptBytes
		for !utf8.RuneStart(s[n]) {
			n--
		}
		e.Text = s[:n]
		e.Truncated = true
	}
	return e
}

// Each PDF runs outside the server: even a tiny compressed PDF can consume
// far more parser memory or CPU than its on-disk size suggests. The worker
// only gets approved bytes, never a user's filename, keys or server settings.
func ExtractReviewExcerpt(ctx context.Context, name string, data []byte) (ReviewExcerpt, error) {
	if len(data) > ReviewFileBytes {
		return ReviewExcerpt{}, ErrExpandedTooLarge
	}
	if strings.EqualFold(filepath.Ext(name), ".pdf") {
		return readReviewPDF(ctx, data)
	}
	s, _, err := ExtractText(name, data)
	if err != nil {
		return ReviewExcerpt{}, err
	}
	if !utf8.ValidString(s) || strings.IndexByte(s, 0) >= 0 {
		return ReviewExcerpt{}, ErrNotText
	}
	return trimReviewText(s), nil
}
func readReviewPDF(ctx context.Context, data []byte) (ReviewExcerpt, error) {
	exe, err := os.Executable()
	if err != nil {
		return ReviewExcerpt{}, ErrParseFailed
	}
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, reviewPDFMode) // #nosec G204 G702 -- self executable, constant private mode, data only on stdin
	cmd.Env = []string{"GOMEMLIMIT=48MiB", "GOMAXPROCS=1", "GOTRACEBACK=none"}
	cmd.Stdin = bytes.NewReader(data)
	out, err := cmd.StdoutPipe()
	if err != nil {
		return ReviewExcerpt{}, ErrParseFailed
	}
	if err := cmd.Start(); err != nil {
		return ReviewExcerpt{}, ErrParseFailed
	}
	const capBytes = 12 << 10
	raw, readErr := io.ReadAll(io.LimitReader(out, capBytes+1))
	if len(raw) > capBytes || readErr != nil {
		cancel()
	}
	err = cmd.Wait()
	var e ReviewExcerpt
	if err != nil || readErr != nil || len(raw) > capBytes || json.Unmarshal(raw, &e) != nil || len(e.Text) > ReviewExcerptBytes {
		return ReviewExcerpt{}, ErrParseFailed
	}
	return e, nil
}

// RunReviewPDFWorker must precede all normal server startup. Parser diagnostics
// must not share the structured result channel or leak document data to logs.
func RunReviewPDFWorker(args []string) (bool, int) {
	if len(args) != 1 || args[0] != reviewPDFMode {
		return false, 0
	}
	return true, reviewPDFWorker()
}
func reviewPDFWorker() (code int) {
	defer func() {
		if recover() != nil {
			code = 2
		}
	}()
	if !limitReviewPDF() {
		return 2
	}
	sink, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		return 2
	}
	defer func() { _ = sink.Close() }()
	out := os.Stdout
	os.Stdout = sink
	data, err := io.ReadAll(io.LimitReader(os.Stdin, ReviewFileBytes+1))
	if err != nil || len(data) > ReviewFileBytes {
		return 2
	}
	r, err := pdf.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return 2
	}
	count := r.NumPage()
	if count < 1 {
		return 2
	}
	var text strings.Builder
	pages := min(count, 8)
	cut := false
	for i := 1; i <= pages; i++ {
		page := r.Page(i)
		if page.V.IsNull() {
			return 2
		}
		part, err := page.GetPlainText(nil)
		if err != nil {
			return 2
		}
		if !utf8.ValidString(part) {
			return 2
		}
		text.WriteString(part)
		text.WriteByte('\n')
		if text.Len() > ReviewExcerptBytes {
			cut = i < count
			break
		}
	}
	e := trimReviewText(text.String())
	e.Truncated = e.Truncated || cut || pages < count
	if err := json.NewEncoder(out).Encode(e); err != nil && !errors.Is(err, io.EOF) {
		return 2
	}
	return 0
}
