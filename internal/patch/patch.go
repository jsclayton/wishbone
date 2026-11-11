// Package patch holds the type for a field of a JSON Merge Patch (RFC 7396)
// body (docs/spec.md, API principles, Writes). A field that is present is
// set, a field that is absent doesn't change, and a field sent as `null` is
// cleared. It imports no HTTP package: a domain package takes these in its
// changes, and internal/server decodes them from the body.
package patch

import (
	"bytes"
	"encoding/json"
)

// Optional is a field of a patch. Its zero value is a field that was absent.
// The JSON decoder only calls UnmarshalJSON for a field that is present, so
// Set tells present from absent.
type Optional[T any] struct {
	// Set is true when the field was in the body.
	Set bool
	// Value is the new value, or nil when the body sent `null`.
	Value *T
}

// Clears reports whether the body sent `null` for the field.
func (o Optional[T]) Clears() bool { return o.Set && o.Value == nil }

// UnmarshalJSON records that the field is present.
func (o *Optional[T]) UnmarshalJSON(b []byte) error {
	o.Set = true
	if bytes.Equal(bytes.TrimSpace(b), []byte("null")) {
		o.Value = nil
		return nil
	}
	var v T
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	o.Value = &v
	return nil
}
