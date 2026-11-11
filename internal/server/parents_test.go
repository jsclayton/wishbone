package server

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// linkParents sets a user's parents as the admin, and fails the test when the
// server refuses.
func (e *env) linkParents(admin, child string, parents ...string) userView {
	e.t.Helper()
	if parents == nil {
		parents = []string{}
	}
	res := e.patchUser(admin, child, map[string]any{"parent_ids": parents}, nil)
	require.Equal(e.t, http.StatusOK, res.StatusCode, "link parents")
	return decode[userView](e.t, res)
}

func summaryNames(list []summaryView) []string {
	out := make([]string, len(list))
	for i, s := range list {
		out[i] = s.Name
	}
	return out
}

func TestTheAdminLinksAParentAndParentAndChildSeeEachOther(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	momID, mom := e.otherUser("Mom")
	emmaID, emma := e.otherUser("Emma")
	_, stranger := e.otherUser("Sue")

	// Before the link, nobody sees across.
	require.Equal(t, http.StatusNotFound, e.getUser(mom, emmaID).StatusCode)

	child := e.linkParents(admin, emmaID, momID)

	require.Equal(t, []string{"Mom"}, summaryNames(child.Parents))
	require.Equal(t, momID, child.Parents[0].ID)
	require.Empty(t, child.Children)
	asParent := decode[userView](t, e.getUser(mom, emmaID))
	require.Equal(t, []string{"Mom"}, summaryNames(asParent.Parents), "a parent sees the child's parents")
	asChild := decode[userView](t, e.getUser(emma, momID))
	require.Equal(t, []string{"Emma"}, summaryNames(asChild.Children), "a child sees the parent's children")
	require.Empty(t, asChild.Parents, "Parents is an empty list, not null")
	require.Equal(t, []string{"Emma", "Mom"}, names(decode[userList](t, e.listUsers(mom, ""))))
	require.Equal(t, []string{"Emma", "Mom"}, names(decode[userList](t, e.listUsers(emma, ""))))
	require.Equal(t, 2, decode[userList](t, e.listUsers(mom, "")).TotalCount)
	require.Equal(t, http.StatusNotFound, e.getUser(stranger, emmaID).StatusCode)
	require.Equal(t, http.StatusNotFound, e.getUser(stranger, momID).StatusCode)
}
