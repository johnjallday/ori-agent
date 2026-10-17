package backuprecoveryhttp

import (
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/johnjallday/ori-agent/internal/backuprecovery"
	"github.com/johnjallday/ori-agent/internal/connectionshttp"
)

// LocalRequestAllowed is shared by the owning endpoints and the generic
// capability lifecycle when it targets Backup & Recovery. Neither a Host nor
// forwarded headers authenticate a remote peer as this Mac's local user.
func LocalRequestAllowed(r *http.Request, mutating bool) bool {
	if r == nil || connectionshttp.NewOriginGuard().Check(r) != nil {
		return false
	}
	peer, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil || net.ParseIP(peer) == nil || !net.ParseIP(peer).IsLoopback() {
		return false
	}
	site := r.Header.Get("Sec-Fetch-Site")
	if site != "" && site != "same-origin" && site != "none" {
		return false
	}
	origins := r.Header.Values("Origin")
	if len(origins) > 1 || mutating && len(origins) != 1 {
		return false
	}
	if len(origins) == 1 {
		origin, err := url.Parse(origins[0])
		scheme := "http"
		if r.TLS != nil {
			scheme = "https"
		}
		target, hostErr := url.Parse(scheme + "://" + r.Host)
		if err != nil || hostErr != nil || origin.Scheme != scheme || origin.User != nil || origin.Opaque != "" || origin.Path != "" || origin.RawQuery != "" || origin.Fragment != "" || !strings.EqualFold(origin.Hostname(), target.Hostname()) || effectivePort(origin) != effectivePort(target) {
			return false
		}
	}
	return !mutating || len(r.Header.Values(ActionHeader)) == 1 && r.Header.Get(ActionHeader) == ActionValue
}

// RespondUserControlError keeps generic lifecycle failures on the same fixed,
// redacted vocabulary as the owning controls.
func RespondUserControlError(w http.ResponseWriter, err error) {
	if err == nil {
		err = backuprecovery.ErrUnavailable
	}
	respondError(w, err)
}

// ParseUserControlBody applies the same bounded exact JSON rules to the generic
// capability install/remove payload. It supplies no additional authority.
func ParseUserControlBody(w http.ResponseWriter, r *http.Request, target any) bool {
	return parseBody(w, r, target)
}
