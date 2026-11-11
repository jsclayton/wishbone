package server

import (
	"net/http"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// These tests close gaps that a review of invitations found.

func TestTwoInvitesOfTheSameUserAtTheSameMomentGiveOne201AndTheRestAlreadyInvited(t *testing.T) {
	e := newEnv(t)
	f := e.family()

	const n = 8
	statuses := make([]int, n)
	codes := make([]string, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res := e.postInvitation(f.mom, f.house, map[string]any{"user_id": f.sueID})
			statuses[i] = res.StatusCode
			if res.StatusCode != http.StatusCreated {
				codes[i] = readProblem(t, res).Code
			}
		}()
	}
	wg.Wait()

	created := 0
	for i, s := range statuses {
		if s == http.StatusCreated {
			created++
			continue
		}
		require.Equal(t, http.StatusConflict, s)
		require.Equal(t, "already_invited", codes[i])
	}
	require.Equal(t, 1, created)
	require.Equal(t, 1, decode[invitationList](t, e.listInvitations(f.sue, "")).TotalCount)
}

func TestAStrangerAsksForTheInvitationsOfAUserAndGetsNone(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	e.invite(f.mom, f.house, f.sueID)
	_, stranger := e.otherUser("Stranger")

	for name, query := range map[string]string{"by user": "user_id=" + f.sueID, "by list": "list_id=" + f.house, "all": ""} {
		res := e.listInvitations(stranger, query)
		require.Equal(t, http.StatusOK, res.StatusCode, name)
		got := decode[invitationList](t, res)
		require.Empty(t, got.Data, name)
		require.Zero(t, got.TotalCount, name)
	}
	// The invited user, and a recipient who sent it, do see it.
	require.Len(t, decode[invitationList](t, e.listInvitations(f.sue, "user_id="+f.sueID)).Data, 1)
	require.Len(t, decode[invitationList](t, e.listInvitations(f.dad, "user_id="+f.sueID)).Data, 1)
}

func TestTheInvitersAvatarStaysHiddenFromStrangers(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	img := e.uploaded(f.mom)
	require.Equal(t, http.StatusOK, e.setAvatar(f.mom, f.momID, img.ID).StatusCode)
	_, stranger := e.otherUser("Stranger")
	require.Equal(t, http.StatusNotFound, e.getImage(stranger, img.ID).StatusCode)

	e.invite(f.mom, f.house, f.sueID)

	// The invitation shows the inviter to the invited user and the recipients only.
	require.NotNil(t, decode[invitationList](t, e.listInvitations(f.sue, "")).Data[0].InvitedBy.Avatar)
	require.Equal(t, http.StatusOK, e.getImage(f.sue, img.ID).StatusCode)
	require.Equal(t, http.StatusOK, e.getImage(f.dad, img.ID).StatusCode)
	for name, token := range map[string]string{"a stranger": stranger, "Hedy": f.hedy} {
		require.Equal(t, http.StatusNotFound, e.getImage(token, img.ID).StatusCode, name)
		require.Empty(t, decode[invitationList](t, e.listInvitations(token, "")).Data, name)
	}
}
