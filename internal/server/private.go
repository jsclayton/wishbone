package server

import (
	"encoding/json"
	"reflect"

	"github.com/danielgtaylor/huma/v2"
)

// Private is a field that only some callers see (docs/spec.md, API
// principles, Values: "A field is absent only when the caller may not see
// it"). The server builds the body for each caller. A caller who may see the
// field gets [Shown], with the value or nil. It is `null` when there is no value.
// A caller who may not see it gets the zero Private, and the key is left out of
// the JSON, not sent as `null`: `null` would say "this user has no timezone",
// which a stranger must not learn. The version of a resource hashes the body as
// the caller sees it, and the keys of the body are the fields the caller sees
// (see [keysOf]), so a hidden field is also not a field to change.
//
// To add a field that only some callers see:
//
//  1. Give the body field the type `Private[T]` and the tag
//     `json:"name,omitzero" required:"false"`. omitzero is what leaves the key out.
//  2. Fill it with [Shown] where the rule says the caller sees it, in one
//     function that names the rule (see seesPrivateFields). Leave it alone otherwise.
//  3. Say who sees it in the doc tag and in docs/routes.md.
type Private[T any] struct {
	shown bool
	value *T
}

// Shown is the field for a caller who sees it. A nil value is sent as `null`.
func Shown[T any](value *T) Private[T] { return Private[T]{shown: true, value: value} }

// IsZero is true for a hidden field. encoding/json leaves out a field with the
// omitzero option when it is true.
func (p Private[T]) IsZero() bool { return !p.shown }

// MarshalJSON writes the value, or null for a field that is shown with no value.
func (p Private[T]) MarshalJSON() ([]byte, error) {
	if p.value == nil {
		return []byte("null"), nil
	}
	return json.Marshal(p.value)
}

// Schema is the schema of T that can also be null. The field is not required,
// because a caller who may not see it gets no key.
func (Private[T]) Schema(r huma.Registry) *huma.Schema {
	s := *r.Schema(reflect.TypeFor[T](), false, "")
	s.Nullable = true
	return &s
}

// PrivateNonNull is a [Private] field that is never `null`: when the caller sees
// it, it has a value, so the schema is optional (the key may be absent) but not
// nullable. Fill it with [ShownValue].
type PrivateNonNull[T any] struct{ Private[T] }

// ShownValue is the field for a caller who sees it.
func ShownValue[T any](value T) PrivateNonNull[T] { return PrivateNonNull[T]{Shown(&value)} }

// Schema is the schema of T. The field is not required, because a caller who may
// not see it gets no key, and it is not nullable.
func (PrivateNonNull[T]) Schema(r huma.Registry) *huma.Schema {
	s := *r.Schema(reflect.TypeFor[T](), false, "")
	return &s
}
