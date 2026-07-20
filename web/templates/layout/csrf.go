// Package layout provides Victus's shared page shell (layout.Base) and
// small cross-cutting template helpers used by every feature area.
package layout

// CSRFFieldName matches gorilla/csrf's default hidden-field name (the
// package default for csrf.FieldName). Every plain HTML <form> in Victus
// includes a hidden input with this name; htmx requests instead carry the
// token via the hx-headers set in Base.
const CSRFFieldName = "gorilla.csrf.Token"
