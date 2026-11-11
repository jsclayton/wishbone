package server

import (
	"fmt"
	"net/http"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"

	"jsclayton.dev/wishbone/internal/item"
	"jsclayton.dev/wishbone/internal/user"
)

func mustID(t *testing.T, s string) int64 {
	t.Helper()
	id, err := strconv.ParseInt(s, 10, 64)
	require.NoError(t, err)
	return id
}

func TestManyAddsOfTheSameImageAtOnceGiveOneItemAndA409ForTheOthers(t *testing.T) {
	e := newEnv(t)
	e.signUp()
	token, listID := e.grace()
	img := e.uploaded(token)

	const n = 8
	statuses := make(chan int, n)
	for i := range n {
		go func() {
			res := e.postItem(token, listID, map[string]any{"title": fmt.Sprintf("Kettle %d", i), "image_ids": imageIDs(img)})
			statuses <- res.StatusCode
		}()
	}
	counts := map[int]int{}
	for range n {
		counts[<-statuses]++
	}

	require.Equal(t, map[int]int{http.StatusCreated: 1, http.StatusConflict: n - 1}, counts)
	require.Len(t, e.order(token, listID), 1)
}

func TestAnImageTakenBetweenTheCheckAndTheWriteIsStillAnImageAttachedError(t *testing.T) {
	e := newEnv(t)
	e.signUp()
	id, grace := e.otherUser("Grace")
	listID := e.primaryListOf(grace).ID
	img := e.uploaded(grace)
	e.addItem(grace, listID, "Kettle", map[string]any{"image_ids": imageIDs(img)})
	viewer, err := user.Get(t.Context(), e.db, mustID(t, id))
	require.NoError(t, err)

	// No guard: the unique index is all that is left to refuse.
	_, err = e.srv.opts.Items.Add(t.Context(), viewer, mustID(t, listID), item.New{Title: "Mug", ImageIDs: []int64{mustID(t, img.ID)}}, nil)

	require.ErrorIs(t, err, item.ErrImageAttached)
}
