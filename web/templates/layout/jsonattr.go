package layout

import "encoding/json"

// JSONAttr marshals v via encoding/json for embedding in an htmx attribute
// like hx-headers or hx-vals — proper JSON encoding rather than string
// concatenation, so a value containing a quote or backslash can't break the
// resulting JSON or smuggle an extra key into it.
func JSONAttr(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(b)
}
