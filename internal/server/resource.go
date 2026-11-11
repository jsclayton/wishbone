package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// versionOf is the version of a resource as one caller sees it: a strong ETag
// (quotes included) that hashes the resource as this caller sees it: the JSON of
// the body struct, without the `$schema` property that huma adds to the bytes it
// sends, and with no regard to the Host or to how the encoder escapes text. It
// is deliberately not a hash of the response bytes. It changes only when
// something that the caller can see changes. A collection row's `version` and
// the resource's `ETag` header hold this same string, so a client can send
// either one back in `If-Match`.
//
// A route defines a row type that embeds the resource's body and adds
// `Version string `json:"version"“, and fills it with this function applied to
// the body without it. The resource's own address has no `version` in its body,
// because the body would hash itself: it sends the value in the `ETag` header.
func versionOf(body any) (string, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("version of a resource: %w", err)
	}
	sum := sha256.Sum256(raw)
	return `"` + hex.EncodeToString(sum[:16]) + `"`, nil
}

// resourceOutput is the output of a route that returns a full resource.
type resourceOutput[T any] struct {
	ETag string `header:"ETag" doc:"The version of the resource as this caller sees it. A hash of the body."`
	Body T
}

// createdOutput is the output of a create: 201, the address of the new
// resource, and the full resource.
type createdOutput[T any] struct {
	Location string `header:"Location" doc:"The address of the new resource."`
	ETag     string `header:"ETag" doc:"The version of the resource as this caller sees it. A hash of the body."`
	Body     T
}

// resourceOf wraps a full resource as the output of a route.
func resourceOf[T any](body T) (*resourceOutput[T], error) {
	v, err := versionOf(body)
	if err != nil {
		return nil, err
	}
	return &resourceOutput[T]{ETag: v, Body: body}, nil
}

// createdOf wraps a new resource as the output of a create. path is its
// address, such as "/users/42".
func createdOf[T any](path string, body T) (*createdOutput[T], error) {
	v, err := versionOf(body)
	if err != nil {
		return nil, err
	}
	return &createdOutput[T]{Location: location(path), ETag: v, Body: body}, nil
}

// resourceChanged is the 412 for an If-Match that no longer fits.
func resourceChanged() error {
	return Error(http.StatusPreconditionFailed, "resource_changed",
		"This changed since you loaded it. Load it again, then repeat the change.")
}

// checkIfMatch compares an If-Match header with the current version of the
// resource, as the caller sees it. A request with no header always passes: the
// merge-patch rules apply, and the last writer wins for each field. It returns
// the 412 problem when no tag in the header is the current version. `*` matches
// any resource that exists, and a weak tag (W/) never matches, as RFC 9110
// says for If-Match. The quotes of a tag are optional, to forgive a client that
// sends a collection row's `version` without them.
func checkIfMatch(header, current string) error {
	if strings.TrimSpace(header) == "" {
		return nil
	}
	for _, tag := range strings.Split(header, ",") {
		tag = strings.TrimSpace(tag)
		if tag == "*" || (!strings.HasPrefix(tag, "W/") && strings.Trim(tag, `"`) == strings.Trim(current, `"`)) {
			return nil
		}
	}
	return resourceChanged()
}
