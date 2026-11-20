package server

import (
	"cmp"
	"context"
	"net/http"
	"slices"

	"github.com/danielgtaylor/huma/v2"

	"jsclayton.dev/wishbone/internal/gift"
	"jsclayton.dev/wishbone/internal/giftflag"
	"jsclayton.dev/wishbone/internal/list"
	"jsclayton.dev/wishbone/internal/occasion"
	"jsclayton.dev/wishbone/internal/user"
)

// This file is GET /versions: the kind, the ID and the version of everything
// the caller can see, in one answer (docs/spec.md, API principles, Versions of
// everything a caller sees).
//
// It holds no rule of who sees what. Each kind is read with the domain call
// that its collection route makes, asked for every row ([collectionRules.all]),
// and turned into rows by the function that the collection route uses. The items
// and the recipients of a list are the exception in how they are read, not in
// what is read: one read for every list ([item.Service.VisibleOnEveryList],
// [list.Service.RecipientsOfEvery]), built from the same conditions as the read
// for one list, because a read for each list is most of the cost on an instance
// with hundreds of lists. So a version here is the `version` of the collection
// row and the `ETag` of the resource's own GET, and a resource is here exactly
// when it is in its collection. TestVersionsListsExactlyWhatTheCallersCollectionsAndGetsGive
// holds this. A new kind of resource adds a function to versionKinds.
//
// Nothing is kept between two calls. What a caller sees can change with no
// write (a gift's `needs_attention`, an occasion's `next_date`), so every call
// builds every row again, also to answer 304. BenchmarkVersions measures it.

// The kinds of resource in the answer, in the order the answer lists them.
const (
	kindUser          = "user"
	kindUserSummary   = "user_summary"
	kindList          = "list"
	kindListRecipient = "list_recipient"
	kindItem          = "item"
	kindStoreLink     = "store_link"
	kindGift          = "gift"
	kindOccasion      = "occasion"
	kindGroup         = "group"
	kindInvitation    = "invitation"
)

var versionKindOrder = []string{kindUser, kindUserSummary, kindList, kindListRecipient, kindItem, kindStoreLink, kindGift, kindOccasion, kindGroup, kindInvitation}

// ResourceVersion is one resource that the caller can see.
type ResourceVersion struct {
	Kind    string  `json:"kind" enum:"user,user_summary,list,list_recipient,item,store_link,gift,occasion,group,invitation" doc:"What the resource is. user_summary is a user whom the caller gets only as a summary inside another resource: GET /users/{id} is a 404 for them."`
	ID      string  `json:"id" example:"42" doc:"The resource's ID: the last part of its address. A list recipient has the ID of its user, so it is one resource only together with list_id."`
	Version string  `json:"version" doc:"The same value as the ETag of the resource's own GET for this caller, and as its version in a collection. A user summary has no address: its version is a hash of the summary."`
	ListID  *string `json:"list_id" nullable:"true" example:"7" doc:"For an item and a list recipient, the list they are on. null for every other kind."`
	ItemID  *string `json:"item_id" nullable:"true" example:"12" doc:"For a store link, its item. null for every other kind."`
}

// VersionsBody is the answer of GET /versions.
type VersionsBody struct {
	Data []ResourceVersion `json:"data" nullable:"false" doc:"Every resource the caller can see, each one once. The order means nothing. A resource that is not here is gone for this caller: deleted, moved out of view, or hidden."`
}

type versionsInput struct {
	IfNoneMatch string `header:"If-None-Match" doc:"The ETag of an earlier answer. When nothing the caller can see changed since, the answer is 304 with no body."`
}

type versionsOutput struct {
	Status int
	ETag   string `header:"ETag" doc:"The version of the whole answer: a hash of the body. Send it back in If-None-Match."`
	Body   VersionsBody
}

// seenVersions gathers the resources of one answer.
type seenVersions struct {
	data []ResourceVersion
	// summaries are the users in the rows the caller gets, as summaries, by ID.
	// The ones the caller doesn't also see in full become user_summary entries.
	summaries map[string]UserSummary
}

func (v *seenVersions) add(kind, id, version string) {
	v.data = append(v.data, ResourceVersion{Kind: kind, ID: id, Version: version})
}

// addOn adds a resource that is on a list or under an item.
func (v *seenVersions) addOn(kind, id, version string, listID, itemID *string) {
	v.data = append(v.data, ResourceVersion{Kind: kind, ID: id, Version: version, ListID: listID, ItemID: itemID})
}

// holds notes the user summaries that a row holds.
func (v *seenVersions) holds(summaries ...UserSummary) {
	for _, s := range summaries {
		v.summaries[s.ID] = s
	}
}

// holdsOptional notes a user summary that can be null.
func (v *seenVersions) holdsOptional(s *UserSummary) {
	if s != nil {
		v.holds(*s)
	}
}

// versionKinds read each kind of resource as the caller sees it. A user summary
// is not one of them: it comes from the rows of the others.
func (s *Server) versionKinds() []func(context.Context, user.User, *seenVersions) error {
	return []func(context.Context, user.User, *seenVersions) error{
		s.userVersions, s.listVersions, s.itemVersions, s.giftVersions, s.occasionVersions, s.groupVersions, s.invitationVersions,
	}
}

// versions is the answer of GET /versions for a caller.
func (s *Server) versions(ctx context.Context, caller user.User) (VersionsBody, error) {
	seen := &seenVersions{data: []ResourceVersion{}, summaries: map[string]UserSummary{}}
	for _, read := range s.versionKinds() {
		if err := read(ctx, caller, seen); err != nil {
			return VersionsBody{}, err
		}
	}
	full := map[string]bool{}
	for _, v := range seen.data {
		if v.Kind == kindUser {
			full[v.ID] = true
		}
	}
	for id, summary := range seen.summaries {
		if full[id] {
			continue
		}
		version, err := versionOf(summary)
		if err != nil {
			return VersionsBody{}, err
		}
		seen.add(kindUserSummary, id, version)
	}
	// One order for one set of resources, so the ETag of the answer moves only
	// when a resource in it does.
	slices.SortFunc(seen.data, func(a, b ResourceVersion) int {
		return cmp.Or(
			cmp.Compare(slices.Index(versionKindOrder, a.Kind), slices.Index(versionKindOrder, b.Kind)),
			compareIDs(deref(a.ListID), deref(b.ListID)),
			compareIDs(deref(a.ItemID), deref(b.ItemID)),
			compareIDs(a.ID, b.ID),
		)
	})
	return VersionsBody{Data: seen.data}, nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// compareIDs orders two IDs as the numbers they are: the shorter first.
func compareIDs(a, b string) int {
	return cmp.Or(cmp.Compare(len(a), len(b)), cmp.Compare(a, b))
}

func (s *Server) userVersions(ctx context.Context, caller user.User, seen *seenVersions) error {
	req, err := userRules.all()
	if err != nil {
		return err
	}
	users, _, _, err := s.opts.Users.Matching(ctx, caller, user.Filter{}, req)
	if err != nil {
		return err
	}
	rows, err := s.userRows(ctx, users, caller)
	if err != nil {
		return err
	}
	for _, r := range rows {
		seen.add(kindUser, r.ID, r.Version)
		seen.holds(r.Parents...)
		seen.holds(r.Children...)
	}
	return nil
}

// listVersions reads the lists and their recipients.
func (s *Server) listVersions(ctx context.Context, caller user.User, seen *seenVersions) error {
	req, err := listRules.all()
	if err != nil {
		return err
	}
	lists, _, _, err := s.opts.Lists.Visible(ctx, caller, list.Filter{}, req)
	if err != nil {
		return err
	}
	rows, err := newListRows(lists, caller)
	if err != nil {
		return err
	}
	// One read for the recipients of every list: a read for each list is most of
	// the cost of the route on an instance with hundreds of lists.
	recipients, err := s.opts.Lists.RecipientsOfEvery(ctx, caller, lists)
	if err != nil {
		return err
	}
	for i, r := range rows {
		seen.add(kindList, r.ID, r.Version)
		seen.holds(r.Recipients...)
		recipientRows, err := newRecipientRows(recipients[lists[i].ID])
		if err != nil {
			return err
		}
		for _, recipient := range recipientRows {
			seen.addOn(kindListRecipient, recipient.User.ID, recipient.Version, &r.ID, nil)
			seen.holds(recipient.User)
		}
	}
	return nil
}

// itemVersions reads the items of every list the caller can see, and their
// store links, in one read for the same reason as the recipients.
func (s *Server) itemVersions(ctx context.Context, caller user.User, seen *seenVersions) error {
	items, err := s.opts.Items.VisibleOnEveryList(ctx, caller)
	if err != nil {
		return err
	}
	rows, err := newItemRows(items)
	if err != nil {
		return err
	}
	for i, r := range rows {
		seen.addOn(kindItem, r.ID, r.Version, &r.ListID, nil)
		seen.holdsOptional(r.AddedBy.UserSummary)
		links, err := newStoreLinkRows(items[i])
		if err != nil {
			return err
		}
		for _, l := range links {
			seen.addOn(kindStoreLink, l.ID, l.Version, nil, &r.ID)
		}
	}
	return nil
}

func (s *Server) giftVersions(ctx context.Context, caller user.User, seen *seenVersions) error {
	req, err := giftRules.all()
	if err != nil {
		return err
	}
	gifts, _, _, err := s.opts.Gifts.Visible(ctx, caller, gift.Filter{}, req)
	if err != nil {
		return err
	}
	rows, err := newGiftRows(gifts)
	if err != nil {
		return err
	}
	for _, r := range rows {
		seen.add(kindGift, r.ID, r.Version)
		seen.holdsOptional(r.Giver.UserSummary)
		if recipients, ok := r.Recipients.Value(); ok {
			seen.holds(recipients...)
		}
		if flags, ok := r.Flags.Value(); ok {
			for _, f := range flags {
				seen.holds(f.users()...)
			}
		}
	}
	return nil
}

// users are the user summaries that the flag shows. They follow the flag's code,
// as [GiftFlag.MarshalJSON] does.
func (f GiftFlag) users() []UserSummary {
	switch f.Code {
	case giftflag.Restored:
		if f.By.UserSummary != nil {
			return []UserSummary{*f.By.UserSummary}
		}
	case giftflag.RecipientsChanged:
		var out []UserSummary
		for _, recipients := range []*[]UserSummary{f.OldRecipients, f.NewRecipients} {
			if recipients != nil {
				out = append(out, *recipients...)
			}
		}
		return out
	}
	return nil
}

func (s *Server) occasionVersions(ctx context.Context, caller user.User, seen *seenVersions) error {
	req, err := occasionRules.all()
	if err != nil {
		return err
	}
	occasions, _, _, err := s.opts.Occasions.Visible(ctx, caller, occasion.Filter{}, req)
	if err != nil {
		return err
	}
	rows, err := newOccasionRows(occasions)
	if err != nil {
		return err
	}
	for _, r := range rows {
		seen.add(kindOccasion, r.ID, r.Version)
		seen.holdsOptional(r.User.UserSummary)
	}
	return nil
}

func (s *Server) groupVersions(ctx context.Context, caller user.User, seen *seenVersions) error {
	req, err := groupRules.all()
	if err != nil {
		return err
	}
	groups, _, _, err := s.opts.Groups.List(ctx, caller, false, req)
	if err != nil {
		return err
	}
	rows, err := newGroupRows(groups, caller)
	if err != nil {
		return err
	}
	for _, r := range rows {
		seen.add(kindGroup, r.ID, r.Version)
	}
	return nil
}

func (s *Server) invitationVersions(ctx context.Context, caller user.User, seen *seenVersions) error {
	req, err := invitationRules.all()
	if err != nil {
		return err
	}
	invitations, _, _, err := s.opts.Lists.Invitations(ctx, caller, list.InvitationFilter{}, req)
	if err != nil {
		return err
	}
	rows, err := newInvitationRows(invitations)
	if err != nil {
		return err
	}
	for _, r := range rows {
		seen.add(kindInvitation, r.ID, r.Version)
		seen.holds(r.User)
		seen.holdsOptional(r.InvitedBy.UserSummary)
	}
	return nil
}

func (s *Server) registerVersions(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "list-versions",
		Method:      http.MethodGet,
		Path:        "/versions",
		Summary:     "The version of everything the caller can see",
		Description: "The kind, the ID and the version of every resource the caller can see: users, lists, list recipients, items, store links, gifts, occasions, groups and invitations. Not sessions, and not passkeys. " +
			"A client that keeps a copy compares the answer with it, gets what differs, and drops what is absent. " +
			"The answer is one response: it has no cursor and no limit, and takes no filter and no sort. " +
			"Its `ETag` is the version of the whole answer. Sent back in `If-None-Match`, it gets a 304 with no body while nothing the caller can see changed. " +
			"A version here moves only when the body of the caller's own `GET` of that resource moves, so the answer shows nothing that a `GET` doesn't.",
		Tags:   []string{"Versions"},
		Errors: []int{http.StatusUnauthorized},
		Responses: map[string]*huma.Response{
			"304": {
				Description: "Nothing the caller can see changed since the answer whose `ETag` is in `If-None-Match`. No body.",
				Headers: map[string]*huma.Param{
					"ETag": {Description: "The version of the whole answer, the same as in If-None-Match.", Schema: &huma.Schema{Type: "string"}},
				},
			},
		},
	}, func(ctx context.Context, in *versionsInput) (*versionsOutput, error) {
		body, err := s.versions(ctx, callerUser(ctx))
		if err != nil {
			return nil, err
		}
		etag, err := versionOf(body)
		if err != nil {
			return nil, err
		}
		if ifNoneMatch(in.IfNoneMatch, etag) {
			return &versionsOutput{Status: http.StatusNotModified, ETag: etag}, nil
		}
		return &versionsOutput{Status: http.StatusOK, ETag: etag, Body: body}, nil
	})
}
