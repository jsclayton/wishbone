package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"sort"

	"github.com/danielgtaylor/huma/v2"

	"jsclayton.dev/wishbone/internal/patch"
)

const mergePatchContentType = "application/merge-patch+json"

// Field is one field of a patch body. A struct tag on it (`nullable`,
// `maxLength`) refines the schema of T.
type Field[T any] struct {
	patch.Optional[T]
}

// Schema is the schema of T. It is a copy, so the field's tags don't change the
// schema that other fields share (`nullable` on a birthday must not make every
// birthday nullable).
func (Field[T]) Schema(r huma.Registry) *huma.Schema {
	s := *r.Schema(reflect.TypeFor[T](), false, "")
	return &s
}

// MergePatch is the body of a PATCH (docs/spec.md, API principles, Writes):
// JSON Merge Patch rules, as `application/merge-patch+json` or as
// `application/json`. T is a struct of [Field]s, one for each field that some
// caller may change. A route reads the fields that the caller may change from
// Changes. Keys is every key of the body, so the route can answer a key that
// isn't a field the caller may change (see [MergePatch.Refuse]).
type MergePatch[T any] struct {
	Keys    []string
	Changes T
}

// UnmarshalJSON reads the body as an object, and records its keys.
func (p *MergePatch[T]) UnmarshalJSON(b []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	p.Keys = p.Keys[:0]
	for k := range raw {
		p.Keys = append(p.Keys, k)
	}
	sort.Strings(p.Keys)
	return json.Unmarshal(b, &p.Changes)
}

// Schema is the schema of T, with room for the keys of the fields that the
// caller sees and can't change: the route answers them, not the schema.
func (MergePatch[T]) Schema(r huma.Registry) *huma.Schema {
	s := r.Schema(reflect.TypeFor[T](), true, "")
	if s.Ref != "" {
		if target := r.SchemaFromRef(s.Ref); target != nil {
			target.AdditionalProperties = true
		}
		return s
	}
	s.AdditionalProperties = true
	return s
}

// Refuse answers the keys of the body that the caller may not set. writable
// are the keys the caller may change, and visible are the keys of the full
// resource that the caller sees. A key the caller sees but may not change is a
// 403 field_not_writable. A key they don't see is indistinguishable from a key
// that is no field at all: a 422 for an unexpected property. A hidden field
// must not show that it exists.
func (p MergePatch[T]) Refuse(writable, visible []string) error {
	var unexpected, locked []string
	for _, k := range p.Keys {
		switch {
		case contains(writable, k):
		case contains(visible, k):
			locked = append(locked, k)
		default:
			unexpected = append(unexpected, k)
		}
	}
	if len(unexpected) > 0 {
		return Error(http.StatusUnprocessableEntity, "invalid_input",
			fmt.Sprintf("There is no field %q to change.", unexpected[0]),
			&huma.ErrorDetail{Location: "body." + unexpected[0], Message: "unexpected property"})
	}
	if len(locked) > 0 {
		return Error(http.StatusForbidden, "field_not_writable",
			fmt.Sprintf("You can see %q, but you can't change it.", locked[0]),
			&huma.ErrorDetail{Location: "body." + locked[0], Message: "not writable by you"})
	}
	return nil
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// keysOf returns the keys of a full resource body as the caller sees it: its
// JSON field names.
func keysOf(body any) ([]string, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("keys of a resource: %w", err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("keys of a resource: %w", err)
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys, nil
}

// acceptMergePatch lets the PATCH operations at the given paths (under the API
// prefix) take their body as `application/merge-patch+json` too, with the same
// schema as `application/json`. Call it after the routes are registered.
func acceptMergePatch(api huma.API, paths ...string) {
	for _, path := range paths {
		item := api.OpenAPI().Paths[apiPrefix+path]
		if item == nil || item.Patch == nil || item.Patch.RequestBody == nil {
			panic("acceptMergePatch: no PATCH operation at " + path)
		}
		content := item.Patch.RequestBody.Content
		if mt := content["application/json"]; mt != nil {
			content[mergePatchContentType] = mt
		}
	}
}
