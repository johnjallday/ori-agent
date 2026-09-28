package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"sort"

	"github.com/johnjallday/ori-agent/internal/workspacecontinuity"
)

// CollectContinuityOwnedFiles inventories the canonical files/ subtree for a
// source preparation attempt. It streams bounded regular-file bytes through
// confined opens; a separate publication owner must recheck the inventory and
// bytes under its lease before reporting Ready. Other folder owners and the
// combined manifest's limits are not covered by this one-owner collector.
func CollectContinuityOwnedFiles(ctx context.Context, folder string) ([]workspacecontinuity.Fingerprint, error) {
	stack := []string{FilesDir}
	files := make([]workspacecontinuity.Fingerprint, 0)
	visited := 0
	var total int64
	for len(stack) != 0 {
		name := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		entries, err := workspacecontinuity.ListCanonicalDirectory(ctx, folder, name, workspacecontinuity.MaxFiles)
		if errors.Is(err, workspacecontinuity.ErrIncomplete) && name == FilesDir {
			return files, nil
		}
		if err != nil {
			return nil, err
		}
		if name != FilesDir && len(entries) == 0 {
			return nil, workspacecontinuity.ErrIncomplete
		}
		for _, entry := range entries {
			visited++
			if visited > workspacecontinuity.MaxFiles {
				return nil, workspacecontinuity.ErrLimit
			}
			location := name + "/" + entry.Name()
			if entry.IsDir() {
				stack = append(stack, location)
				continue
			}
			if !entry.Type().IsRegular() {
				return nil, workspacecontinuity.ErrUnsafe
			}
			fingerprint, err := fingerprintContinuityOwnedFile(ctx, folder, location)
			if err != nil {
				return nil, err
			}
			if fingerprint.Bytes > workspacecontinuity.MaxTotalBytes-total {
				return nil, workspacecontinuity.ErrLimit
			}
			total += fingerprint.Bytes
			files = append(files, fingerprint)
		}
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	if err := CheckContinuityFilesCoverage(ctx, folder, files); err != nil {
		return nil, err
	}
	return files, nil
}

func fingerprintContinuityOwnedFile(ctx context.Context, folder, location string) (workspacecontinuity.Fingerprint, error) {
	file, err := workspacecontinuity.OpenCanonicalFile(folder, location)
	if err != nil {
		return workspacecontinuity.Fingerprint{}, err
	}
	defer func() { _ = file.Close() }()
	before, err := file.Stat()
	if err != nil || !before.Mode().IsRegular() {
		return workspacecontinuity.Fingerprint{}, workspacecontinuity.ErrUnsafe
	}
	if before.Size() < 0 || before.Size() > workspacecontinuity.MaxBlobBytes {
		return workspacecontinuity.Fingerprint{}, workspacecontinuity.ErrLimit
	}
	hash := sha256.New()
	buf := make([]byte, 32<<10)
	var length int64
	for {
		if err := ctx.Err(); err != nil {
			return workspacecontinuity.Fingerprint{}, err
		}
		n, err := file.Read(buf)
		if n == 0 && err == nil {
			return workspacecontinuity.Fingerprint{}, workspacecontinuity.ErrIncomplete
		}
		if int64(n) > workspacecontinuity.MaxBlobBytes-length {
			return workspacecontinuity.Fingerprint{}, workspacecontinuity.ErrLimit
		}
		length += int64(n)
		if _, hashErr := hash.Write(buf[:n]); hashErr != nil {
			return workspacecontinuity.Fingerprint{}, hashErr
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return workspacecontinuity.Fingerprint{}, workspacecontinuity.ErrUnsafe
		}
	}
	if err := ctx.Err(); err != nil {
		return workspacecontinuity.Fingerprint{}, err
	}
	after, err := file.Stat()
	if err != nil || !os.SameFile(before, after) || after.Size() != length || before.Size() != length || !after.ModTime().Equal(before.ModTime()) {
		return workspacecontinuity.Fingerprint{}, workspacecontinuity.ErrChanged
	}
	return workspacecontinuity.Fingerprint{Path: location, Digest: hex.EncodeToString(hash.Sum(nil)), Bytes: length}, nil
}
