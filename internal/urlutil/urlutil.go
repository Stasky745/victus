// Package urlutil holds small URL-validation helpers shared across config
// loading and user-facing form validation, so both trust boundaries apply
// the same rules instead of drifting apart.
package urlutil

import "net/url"

// IsAbsoluteHTTP reports whether raw is a well-formed absolute http(s) URL
// — a real scheme (http/https) and a non-empty host, not just a string that
// happens to start with the right prefix.
func IsAbsoluteHTTP(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	return (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}
