package learningmissionshttp

import (
	"errors"
	"mime"
	"net/http"
	"net/url"

	"github.com/johnjallday/ori-agent/internal/connectionshttp"
	"github.com/johnjallday/ori-agent/internal/userprofile"
)

var (
	ErrOriginUnavailable = errors.New("request origin unavailable")
	ErrJSONRequired      = errors.New("JSON is required")
	ErrOwnerUnavailable  = errors.New("owner unavailable")
)

// LocalOwner is shared by learning and its existing Ticket execution review.
// It is a browser-origin boundary, not authentication against local processes.
func LocalOwner(r *http.Request, users userprofile.UserProvider, mutation bool) (string, error) {
	if connectionshttp.NewOriginGuard().Check(r) != nil {
		return "", ErrOriginUnavailable
	}
	origin := r.Header.Get("Origin")
	if origin != "" || mutation {
		u, err := url.Parse(origin)
		scheme := "http"
		if r.TLS != nil {
			scheme = "https"
		}
		if err != nil || u.Host != r.Host || u.Scheme != scheme || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
			return "", ErrOriginUnavailable
		}
	}
	if mutation {
		kind, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || kind != "application/json" {
			return "", ErrJSONRequired
		}
	}
	if users == nil {
		return "", ErrOwnerUnavailable
	}
	owner, err := users.CurrentUserID(r.Context())
	if err != nil || owner == "" {
		return "", ErrOwnerUnavailable
	}
	return owner, nil
}
