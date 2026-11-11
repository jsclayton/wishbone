package server

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// These tests cover the references to a deleted user that stay as they are, or
// become null (docs/spec.md, Deactivation and deletion, Deletion).

func TestAReferenceToADeletedUserBecomesNullOrLeavesTheList(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	e.join(f.admin, e.cousins(f), f.dadID, f.hedyID)
	// Dad added an item with a picture to the list he shares with Mom, and sent an
	// invitation. Sue gave Mom and Dad a kettle, and Dad says it did not arrive.
	photo := e.uploaded(f.dad)
	grill := e.addItem(f.dad, f.house, "Grill", map[string]any{"image_ids": imageIDs(photo)})
	e.invite(f.dad, f.house, f.hedyID)
	kettle := e.addItem(f.mom, f.house, "Kettle")
	given := e.given(f.sue, kettle.ID, nil)
	require.Equal(t, http.StatusNoContent, e.restore(f.dad, given.ID).StatusCode)
	require.Equal(t, f.dadID, flagOf(t, e.gift(f.sue, given.ID), "restored")["by"].(map[string]any)["id"])
	// A recipient joined the list after Sue's gift was made, so the gift is flagged too.
	zedID, zed := e.cousin("Zed", f)
	e.joinList(f.mom, f.house, zedID, zed)
	unsent := e.uploaded(f.dad)
	require.Equal(t, 2, e.rowCount("images", `detached_at IS NULL AND uploaded_by = ?`, f.dadID), "the photo, and the one Dad never used")

	e.deleteUser(f.dadID)

	// The item stays on the list, with no adder, and its picture loads for Mom.
	item := decode[itemView](t, e.getItem(f.mom, grill.ID))
	require.Nil(t, item.AddedBy)
	require.Equal(t, http.StatusOK, e.getImage(f.mom, photo.ID).StatusCode)
	require.Zero(t, e.rowCount("images", `id = ? AND uploaded_by IS NOT NULL`, photo.ID), "it has no uploader now")
	require.False(t, e.row(photo.ID).Detached, "another resource holds it")
	// The invitation stays, with no inviter.
	inv := decode[invitationList](t, e.listInvitations(f.hedy, ""))
	require.Len(t, inv.Data, 1)
	require.Nil(t, inv.Data[0].InvitedBy)
	// The restored flag stays, with no user.
	flag := flagOf(t, e.gift(f.sue, given.ID), "restored")
	require.Contains(t, flag, "by")
	require.Nil(t, flag["by"])
	// The recipients-changed flag drops Dad from its old and its new recipients.
	oldIDs, newIDs := recipientsFlag(t, e.gift(f.sue, given.ID))
	require.Equal(t, []string{f.momID}, oldIDs)
	require.Equal(t, []string{f.momID, zedID}, newIDs)
	// What he never used goes to the sweep at once, and what is held stays.
	require.True(t, e.row(unsent.ID).Detached)
	require.Equal(t, 1, e.sweep())
	require.Equal(t, http.StatusOK, e.getImage(f.mom, photo.ID).StatusCode)
}

func TestAnAvatarThatTheDeletedUserUploadedForAnotherUserStaysWithThatUser(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	emmaID, emma := e.otherUser("Emma")
	e.linkParents(f.admin, emmaID, f.dadID)
	// Dad uploaded Emma's avatar for her, and Emma's own avatar is hers.
	avatar := e.uploaded(f.dad)
	require.Equal(t, http.StatusOK, e.setAvatar(f.dad, emmaID, avatar.ID).StatusCode)
	photo := e.addItem(emma, e.primaryListFor(emma, emmaID), "Book", map[string]any{"image_ids": imageIDs(e.uploaded(emma))})

	e.deleteUser(f.dadID)

	// Dad's delete doesn't take the avatar of Emma, who is still there.
	got := decode[userView](t, e.getUser(f.admin, emmaID))
	require.NotNil(t, got.Avatar)
	require.Equal(t, avatar.ID, got.Avatar.ID)
	require.Equal(t, http.StatusOK, e.getImage(emma, avatar.ID).StatusCode)
	require.Equal(t, http.StatusOK, e.getItem(emma, photo.ID).StatusCode)
	require.Zero(t, e.sweep(), "nothing the sweep may delete")
}
