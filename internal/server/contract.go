package server

import (
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/danielgtaylor/huma/v2"
)

// The OpenAPI document is the contract (docs/spec.md, Versions of the API).
// The spec lets the server add response fields, enum values and error codes
// within an API version, and tells a client to accept them. This file makes
// the document say the same, so a client that is built from the document, or
// checked against it, doesn't break on a change that the spec allows:
//
//   - A response object allows properties that it doesn't list, and an enum in
//     a response is open ([openResponses]). A request stays as strict as the
//     server is.
//   - An operation lists the codes that it can answer with, by status, in
//     `x-error-codes`, and its error responses are the statuses of those codes
//     ([documentAnswers]). A route names the codes of its own work with
//     [withCodes]. A step that many routes share names its own with [declareCodes]
//     where it joins the operation, and [sharedAnswers] reads the rest from
//     the operation's shape.
//
// [finishContract] runs both, after every route is registered.

// errorCodesExtension is where the document lists the codes of the problems
// that an operation can answer with, by status, each list in order:
// {"403": ["forbidden", "user_deactivated"]}. docs/routes.md gives the same
// codes for each route, and TestErrorCodesMatchTheRouteTable holds the two
// equal. At the top of the document, it lists the codes of an address that
// has no operation ([addressAnswers]).
const errorCodesExtension = "x-error-codes"

// errorAnswer is one error answer of the API: a status, and the `code` of its
// problem.
type errorAnswer struct {
	status int
	code   string
}

// codeStatus is every code that an operation can be given by name, with the
// status that the code always comes with. A new code goes here, in the row of
// its route in docs/routes.md, and in that route's [withCodes].
//
// `bad_request` is not here, because it comes with more than one status: it
// is the code of every 4xx that has no code of its own (see [defaultCode]).
// [sharedAnswers] gives it to the operations that can answer with it.
var codeStatus = map[string]int{
	// The codes of docs/routes.md, "Codes on every route".
	"unauthorized":           http.StatusUnauthorized,
	"forbidden":              http.StatusForbidden,
	"field_not_writable":     http.StatusForbidden,
	"user_deactivated":       http.StatusForbidden,
	"reference_not_allowed":  http.StatusForbidden,
	"not_found":              http.StatusNotFound,
	"idempotency_key_in_use": http.StatusConflict,
	"resource_changed":       http.StatusPreconditionFailed,
	"request_too_large":      http.StatusRequestEntityTooLarge,
	"invalid_input":          http.StatusUnprocessableEntity,
	"unknown_filter":         http.StatusUnprocessableEntity,
	"unknown_sort":           http.StatusUnprocessableEntity,
	"cursor_mismatch":        http.StatusUnprocessableEntity,
	"idempotency_key_reused": http.StatusUnprocessableEntity,
	"reference_not_found":    http.StatusUnprocessableEntity,
	"rate_limited":           http.StatusTooManyRequests,
	"internal_error":         http.StatusInternalServerError,

	// Health, setup and sign-in.
	"service_unavailable":  http.StatusServiceUnavailable,
	"setup_complete":       http.StatusConflict,
	"setup_code_invalid":   http.StatusUnprocessableEntity,
	"login_link_invalid":   http.StatusUnauthorized,
	"passkey_login_failed": http.StatusUnauthorized,

	// Users and passkeys.
	"admin_cannot_be_deactivated":  http.StatusForbidden,
	"birthday_required":            http.StatusUnprocessableEntity,
	"parent_loop":                  http.StatusUnprocessableEntity,
	"admin_has_no_parents":         http.StatusUnprocessableEntity,
	"passkey_registration_expired": http.StatusBadRequest,
	"passkey_not_verified":         http.StatusBadRequest,
	"passkey_already_added":        http.StatusConflict,

	// Groups and blocked stores.
	"already_in_group": http.StatusConflict,
	"group_loop":       http.StatusUnprocessableEntity,
	"already_blocked":  http.StatusConflict,

	// Lists and invitations.
	"primary_list":              http.StatusForbidden,
	"list_has_other_recipients": http.StatusForbidden,
	"last_recipient":            http.StatusForbidden,
	"already_invited":           http.StatusConflict,
	"already_recipient":         http.StatusConflict,

	// Items, store links, search and images.
	"cannot_edit_target_list": http.StatusForbidden,
	"image_attached":          http.StatusConflict,
	"image_too_large":         http.StatusRequestEntityTooLarge,
	"range_not_satisfiable":   http.StatusRequestedRangeNotSatisfiable,
	"too_many_store_links":    http.StatusUnprocessableEntity,
	"too_many_images":         http.StatusUnprocessableEntity,
	"store_blocked":           http.StatusUnprocessableEntity,
	"image_invalid":           http.StatusUnprocessableEntity,
	"image_fetch_failed":      http.StatusUnprocessableEntity,

	// Occasions.
	"birthday":             http.StatusForbidden,
	"occasion_is_everyone": http.StatusConflict,
	"already_attached":     http.StatusConflict,

	// Gifts.
	"gift_given":                 http.StatusForbidden,
	"recipient_cannot_claim":     http.StatusForbidden,
	"quantity_raise_not_allowed": http.StatusForbidden,
	"item_fully_claimed":         http.StatusConflict,
	"already_claimed":            http.StatusConflict,
	"occasion_not_allowed":       http.StatusUnprocessableEntity,
	"occasion_locked":            http.StatusUnprocessableEntity,
}

// addressAnswers are the answers of an address that has no operation: a path
// that nothing is at, a method that the address doesn't take, and a retired
// API version ([retiredAPIVersions]). They are at the top of the document,
// because they belong to no operation.
var addressAnswers = []errorAnswer{
	{http.StatusNotFound, "not_found"},
	{http.StatusMethodNotAllowed, "method_not_allowed"},
	{http.StatusGone, "api_version_removed"},
}

// answersKey is the key of an operation's Metadata that holds its answers
// from registration until [documentAnswers] writes them in the document.
const answersKey = "answers"

// withCodes returns the operation with the codes that the route's own work can
// answer with, each by its name in [codeStatus]:
//
//	huma.Register(api, withCodes(huma.Operation{...}, "not_found", "gift_given"), ...)
//
// Name what the route's handler, and the domain calls it makes, can answer
// with. Leave out what the route gets from a step that it shares with other
// routes, which the step or [sharedAnswers] adds: the sign-in, the
// `Idempotency-Key`, the rules of a collection, `If-Match`, the body and the
// parameters that huma checks, and the 500.
//
// It also sets the operation's Errors to the statuses of those codes, as huma
// expects.
func withCodes(op huma.Operation, names ...string) huma.Operation {
	declareCodes(&op, names...)
	return op
}

// declareCodes adds codes to the answers of an operation. A step that many
// routes share calls it where it joins the operation, such as a modifier of
// the signed-in group. A code that is already there is not added again, so a
// modifier that huma runs more than once is safe.
func declareCodes(op *huma.Operation, names ...string) {
	for _, name := range names {
		status, ok := codeStatus[name]
		if !ok {
			// A route names a code that codeStatus doesn't have. Refuse it when
			// the route is registered, not on a request.
			panic("server: " + op.OperationID + " names the unknown code " + name)
		}
		have, _ := op.Metadata[answersKey].([]errorAnswer)
		if a := (errorAnswer{status, name}); !slices.Contains(have, a) {
			if op.Metadata == nil {
				op.Metadata = map[string]any{}
			}
			// A new slice each time: a copy of the operation shares the old one.
			op.Metadata[answersKey] = append(slices.Clone(have), a)
		}
		if !slices.Contains(op.Errors, status) {
			op.Errors = append(slices.Clone(op.Errors), status)
		}
	}
}

// sharedAnswers are the answers that an operation has because of its shape,
// and not because of its own work. They are read from the operation as it is
// in the document, so no route can leave one out:
//
//   - Any route can fail in a way the caller can't fix: 500.
//   - A route that takes `If-Match` answers 412 ([checkIfMatch]).
//   - huma checks each parameter against its schema, and answers 422.
//   - huma reads a body up to the route's limit (413), and for at most the
//     route's read timeout (408). An empty body where one is required is a
//     400. A body that huma parses is a 400 when it can't be read as its
//     type, a 415 when its type is one that the server doesn't know, and a
//     422 when it breaks its schema.
//
// The 4xx statuses that have no code of their own carry `bad_request`.
func sharedAnswers(op *huma.Operation) []errorAnswer {
	answers := []errorAnswer{{http.StatusInternalServerError, "internal_error"}}
	if slices.ContainsFunc(op.Parameters, func(p *huma.Param) bool { return p.In == "header" && p.Name == "If-Match" }) {
		answers = append(answers, errorAnswer{http.StatusPreconditionFailed, "resource_changed"})
	}
	if slices.ContainsFunc(op.Parameters, canBeInvalid) {
		answers = append(answers, errorAnswer{http.StatusUnprocessableEntity, "invalid_input"})
	}
	if body := op.RequestBody; body != nil {
		// A route that reads the body itself sets SkipValidateBody.
		parsed := !op.SkipValidateBody
		answers = append(answers,
			errorAnswer{http.StatusRequestEntityTooLarge, "request_too_large"},
			errorAnswer{http.StatusRequestTimeout, "bad_request"})
		if body.Required || parsed {
			answers = append(answers, errorAnswer{http.StatusBadRequest, "bad_request"})
		}
		if parsed {
			answers = append(answers,
				errorAnswer{http.StatusUnsupportedMediaType, "bad_request"},
				errorAnswer{http.StatusUnprocessableEntity, "invalid_input"})
		}
	}
	return answers
}

// canBeInvalid says whether a request can break the schema of a parameter. A
// plain string in the path or the query can't: any text is a string. A
// parameter that can't be invalid gives its operation no 422.
func canBeInvalid(p *huma.Param) bool {
	s := p.Schema
	if s == nil || s.Ref != "" || (p.Required && p.In != "path") {
		return true
	}
	return s.Type != huma.TypeString || len(s.Enum) > 0 || s.Format != "" || s.Pattern != "" ||
		s.MinLength != nil || s.MaxLength != nil
}

// documentAnswers writes the answers of an operation in the document: one
// error response for each status, and the codes by status in `x-error-codes`.
// The answers are the ones the operation was given ([withCodes], [declareCodes])
// and the ones of its shape ([sharedAnswers]). A 413 with a code of the
// route's own, such as `image_too_large`, replaces `request_too_large`.
//
// It replaces every error response that huma wrote, so the document has no
// status without a code, and no `default` response.
func documentAnswers(op *huma.Operation, problem map[string]*huma.MediaType) {
	given, _ := op.Metadata[answersKey].([]errorAnswer)
	answers := slices.Clone(given)
	ownLimit := slices.ContainsFunc(given, func(a errorAnswer) bool { return a.status == http.StatusRequestEntityTooLarge })
	for _, a := range sharedAnswers(op) {
		if a.code == "request_too_large" && ownLimit {
			continue
		}
		if !slices.Contains(answers, a) {
			answers = append(answers, a)
		}
	}

	for status := range op.Responses {
		if n, err := strconv.Atoi(status); err != nil || n >= 400 {
			delete(op.Responses, status)
		}
	}
	byStatus := codesByStatus(answers)
	for status, names := range byStatus {
		n, _ := strconv.Atoi(status)
		op.Responses[status] = &huma.Response{
			Description: http.StatusText(n) + ". Codes: `" + strings.Join(names, "`, `") + "`.",
			Content:     problem,
		}
	}
	if op.Extensions == nil {
		op.Extensions = map[string]any{}
	}
	op.Extensions[errorCodesExtension] = byStatus
}

// codesByStatus is the form of `x-error-codes`: the codes of each status, in
// order, under the status as a string.
func codesByStatus(answers []errorAnswer) map[string][]string {
	out := map[string][]string{}
	for _, a := range answers {
		status := strconv.Itoa(a.status)
		out[status] = append(out[status], a.code)
	}
	for _, names := range out {
		sort.Strings(names)
	}
	return out
}

// contractDescription is the description of the API in the document. It says
// in words what the document says in its schemas and extensions.
const contractDescription = "The API of a Wishbone instance. This document is its contract.\n\n" +
	"Within an API version, the server can add routes, response fields, optional request fields, filters, enum values, and error codes. " +
	"A client must ignore a response field that it doesn't know, accept an enum value that it doesn't know, " +
	"show `detail` for a `code` that it doesn't know, and always follow `next_cursor`. The document says the same:\n\n" +
	"- A response object allows properties that it doesn't list.\n" +
	"- An enum in a response is `anyOf` the values that it has today and any string. An enum in a request lists the only values that the server takes.\n" +
	"- An error is `application/problem+json` with a `code`. On an operation, `" + errorCodesExtension + "` lists the codes that it can answer with, by status. " +
	"At the top of the document, `" + errorCodesExtension + "` lists the codes of an address that has no operation: " +
	"a path that nothing is at, a method that the address doesn't take, and a removed API version such as `/api/v1`."

// finishContract makes the document say what the spec allows. Call it once,
// after every route is registered and every change to an operation is made.
func finishContract(api huma.API) {
	oapi := api.OpenAPI()
	oapi.Info.Description = contractDescription
	if oapi.Extensions == nil {
		oapi.Extensions = map[string]any{}
	}
	oapi.Extensions[errorCodesExtension] = codesByStatus(addressAnswers)

	problem := map[string]*huma.MediaType{
		"application/problem+json": {Schema: oapi.Components.Schemas.Schema(reflect.TypeFor[Problem](), true, "")},
	}
	for _, op := range operations(oapi) {
		documentAnswers(op, problem)
	}
	openResponses(oapi)
}

// operations are the operations of the document, in the order of their IDs.
func operations(oapi *huma.OpenAPI) []*huma.Operation {
	var ops []*huma.Operation
	for _, item := range oapi.Paths {
		for _, op := range []*huma.Operation{item.Get, item.Put, item.Post, item.Delete, item.Options, item.Head, item.Patch, item.Trace} {
			if op != nil {
				ops = append(ops, op)
			}
		}
	}
	sort.Slice(ops, func(i, j int) bool { return ops[i].OperationID < ops[j].OperationID })
	return ops
}

// openResponses opens every schema that a response can hold:
//
//   - An object allows properties that it doesn't list. huma closes every
//     struct (`additionalProperties: false`), which is right for a request
//     and wrong for a response that can get a new field. The keyword is left
//     out, which allows any other property, and makes a generated client
//     ignore one where `true` would make it collect them.
//   - An enum becomes `anyOf` its values and any value of its type, so a
//     generated client has a case for a value that it doesn't know.
//
// The schemas of requests stay as they are: their `enum` tags are what huma
// checks input with. So a schema can't be in both. A type that a request and a
// response share needs two Go types, as RuleInput and RuleBody are, and this
// panics until it has them. It runs when the server is built, so every test
// finds it.
func openResponses(oapi *huma.OpenAPI) {
	registry := oapi.Components.Schemas
	requests, responses := map[*huma.Schema]bool{}, map[*huma.Schema]bool{}
	for _, op := range operations(oapi) {
		for _, p := range op.Parameters {
			reach(registry, requests, p.Schema)
		}
		if op.RequestBody != nil {
			for _, media := range op.RequestBody.Content {
				reach(registry, requests, media.Schema)
			}
		}
		for _, res := range op.Responses {
			for _, h := range res.Headers {
				reach(registry, responses, h.Schema)
			}
			for _, media := range res.Content {
				reach(registry, responses, media.Schema)
			}
		}
	}

	var shared []string
	for s := range responses {
		if isClosed(s) && requests[s] {
			shared = append(shared, describeSchema(registry, s))
		}
	}
	if len(shared) > 0 {
		sort.Strings(shared)
		panic(fmt.Sprintf("server: a request and a response share a schema that only the response may open: %s. Give the request a type of its own.",
			strings.Join(shared, "; ")))
	}
	for s := range responses {
		openSchema(s)
	}
}

// reach adds a schema, and every schema under it, to the set. It follows a
// reference to the schema it names.
func reach(registry huma.Registry, set map[*huma.Schema]bool, s *huma.Schema) {
	if s == nil || set[s] {
		return
	}
	set[s] = true
	if s.Ref != "" {
		reach(registry, set, registry.SchemaFromRef(s.Ref))
	}
	for _, p := range s.Properties {
		reach(registry, set, p)
	}
	reach(registry, set, s.Items)
	if extra, ok := s.AdditionalProperties.(*huma.Schema); ok {
		reach(registry, set, extra)
	}
	for _, list := range [][]*huma.Schema{s.OneOf, s.AnyOf, s.AllOf} {
		for _, sub := range list {
			reach(registry, set, sub)
		}
	}
	reach(registry, set, s.Not)
}

// isClosed says whether [openSchema] changes the schema.
func isClosed(s *huma.Schema) bool {
	extra, isBool := s.AdditionalProperties.(bool)
	return (isBool && !extra) || len(s.Enum) > 0
}

// openSchema opens one schema of a response (see [openResponses]).
func openSchema(s *huma.Schema) {
	if extra, isBool := s.AdditionalProperties.(bool); isBool && !extra {
		s.AdditionalProperties = nil
	}
	if len(s.Enum) > 0 {
		// The values of today, or any value of the type. A null goes with the
		// second, which takes everything the first doesn't.
		s.AnyOf = []*huma.Schema{
			{Type: s.Type, Enum: s.Enum},
			{Type: s.Type, Nullable: s.Nullable},
		}
		s.Type, s.Enum, s.Nullable = "", nil, false
	}
}

// describeSchema names a schema for the panic of [openResponses]: its name in
// the document, or what it holds when it has none.
func describeSchema(registry huma.Registry, s *huma.Schema) string {
	for name, named := range registry.Map() {
		if named == s {
			return name
		}
	}
	if len(s.Enum) > 0 {
		return fmt.Sprintf("an enum of %v", s.Enum)
	}
	keys := make([]string, 0, len(s.Properties))
	for k := range s.Properties {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return "an object with " + strings.Join(keys, ", ")
}
