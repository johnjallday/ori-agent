package workspacesurfacehttp

import (
	"errors"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// Opaque-origin WebKit frames cannot resolve CSP 'self' to their asset origin.
// Pin executable/style resources to this host's random, versioned frame path
// instead. Even scheme-less sources are ambiguous to an opaque WebKit origin.
func frameAssetPolicy(host, scheme, frameToken, assetVersion string) (string, error) {
	if (scheme != "http" && scheme != "https") || frameToken == "" || assetVersion == "" {
		return "", errors.New("invalid frame asset origin or identity")
	}
	for _, char := range host {
		if !((char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') ||
			(char >= '0' && char <= '9') || strings.ContainsRune(".-:[]", char)) {
			return "", errors.New("invalid frame asset host")
		}
	}
	parsed, err := url.Parse("http://" + host)
	if err != nil || parsed.Host != host || parsed.User != nil || parsed.Hostname() == "" || parsed.Path != "" {
		return "", errors.New("invalid frame asset host")
	}
	if port := parsed.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return "", errors.New("invalid frame asset port")
		}
	} else if strings.HasSuffix(host, ":") {
		return "", errors.New("empty frame asset port")
	}
	if net.ParseIP(parsed.Hostname()) == nil {
		if len(parsed.Hostname()) > 253 {
			return "", errors.New("invalid frame asset hostname")
		}
		for _, label := range strings.Split(strings.TrimSuffix(parsed.Hostname(), "."), ".") {
			if label == "" || len(label) > 63 || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") || strings.ContainsAny(label, ":[]") {
				return "", errors.New("invalid frame asset hostname")
			}
		}
	}
	prefix := scheme + "://" + host + "/api/workspace-surfaces/frames/" + url.PathEscape(frameToken) + "/" + url.PathEscape(assetVersion) + "/"
	return "default-src 'none'; script-src " + prefix + "; style-src " + prefix + "; img-src " + prefix + " data:; font-src " + prefix + "; connect-src 'none'; object-src 'none'; base-uri 'none'; form-action 'none'; frame-ancestors 'self'", nil
}

// Bind the explicit asset scheme at the authenticated parent-side POST, not
// from an opaque subresource's null Origin or untrusted forwarding headers.
// A same-host browser Origin also preserves HTTPS through TLS termination.
func frameRequestScheme(r *http.Request) (string, error) {
	if origin := r.Header.Get("Origin"); origin != "" {
		parsed, err := url.Parse(origin)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") ||
			parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
			return "", errors.New("invalid surface parent origin")
		}
		requested, err := url.Parse(parsed.Scheme + "://" + r.Host)
		port := func(value *url.URL) string {
			if value.Port() != "" {
				return value.Port()
			}
			if value.Scheme == "https" {
				return "443"
			}
			return "80"
		}
		if err != nil || requested.Hostname() == "" || !strings.EqualFold(parsed.Hostname(), requested.Hostname()) || port(parsed) != port(requested) {
			return "", errors.New("mismatched surface parent authority")
		}
		return parsed.Scheme, nil
	}
	if r.TLS != nil {
		return "https", nil
	}
	return "http", nil
}
