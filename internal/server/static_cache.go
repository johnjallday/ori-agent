package server

import (
	"crypto/sha256"
	"encoding/hex"
	"sync"
)

// staticETags memoizes content-hash ETags for embedded static assets keyed by
// request path. The embedded filesystem is immutable for the lifetime of the
// process, so each asset's ETag only needs to be computed once.
var staticETags sync.Map // map[string]string

// staticETag returns a strong, quoted ETag derived from the asset content.
// Because the value is content-derived, it changes automatically whenever the
// embedded asset changes (i.e. on a new build), giving correct revalidation in
// both development and release builds without any cache-busting query strings.
func staticETag(path string, content []byte) string {
	if v, ok := staticETags.Load(path); ok {
		return v.(string)
	}
	sum := sha256.Sum256(content)
	etag := `"` + hex.EncodeToString(sum[:16]) + `"`
	staticETags.Store(path, etag)
	return etag
}
