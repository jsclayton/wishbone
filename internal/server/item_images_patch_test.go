package server

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// These tests cover PATCH /items/{id} with image_ids and primary_image_id
// (docs/spec.md, Items, Images on an item).

func (e *env) patchImages(token, itemID string, body map[string]any) *http.Response {
	e.t.Helper()
	return e.patchItem(token, itemID, body, nil)
}

func TestImageIDsReplacesTheWholeArrayAndTheServerDeletesTheImagesLeftOut(t *testing.T) {
	e := newEnv(t)
	e.signUp()
	token, listID := e.grace()
	a, b, c, d := e.uploaded(token), e.uploaded(token), e.uploaded(token), e.uploaded(token)
	it := e.addItem(token, listID, "Kettle", map[string]any{"image_ids": imageIDs(a, b, c)})
	rowB := e.row(b.ID)

	res := e.patchImages(token, it.ID, map[string]any{"image_ids": imageIDs(c, d, a)})
	require.Equal(t, http.StatusOK, res.StatusCode)
	got := decode[itemView](t, res)

	require.Equal(t, []string{c.ID, d.ID, a.ID}, got.imageIDs(), "the order given")
	require.Equal(t, a.ID, got.PrimaryImage.ID, "the primary image stays when it is kept")
	require.Equal(t, res.Header.Get("ETag"), e.getItem(token, it.ID).Header.Get("ETag"))
	require.Equal(t, got.Images, decode[itemView](t, e.getItem(token, it.ID)).Images)

	require.Equal(t, 1, e.sweep(), "the image left out is deleted")
	require.False(t, e.hasFile(rowB))
	require.Equal(t, http.StatusNotFound, e.getImage(token, b.ID).StatusCode)
	require.Equal(t, http.StatusOK, e.getImage(token, d.ID).StatusCode)
}

func TestImageIDsCanAddTheFirstImagesAndRemoveEveryImage(t *testing.T) {
	e := newEnv(t)
	e.signUp()
	token, listID := e.grace()
	a, b := e.uploaded(token), e.uploaded(token)
	it := e.addItem(token, listID, "Kettle")

	got := decode[itemView](t, e.patchImages(token, it.ID, map[string]any{"image_ids": imageIDs(a, b)}))
	require.Equal(t, []string{a.ID, b.ID}, got.imageIDs())
	require.Equal(t, a.ID, got.PrimaryImage.ID, "the first image added is the primary image")

	got = decode[itemView](t, e.patchImages(token, it.ID, map[string]any{"image_ids": []string{}}))
	require.Empty(t, got.Images)
	require.NotNil(t, got.Images)
	require.Nil(t, got.PrimaryImage)
	require.Equal(t, 2, e.sweep())
}

func TestPrimaryImageIDPicksThePrimaryImageAndDoesNotChangeTheOrder(t *testing.T) {
	e := newEnv(t)
	e.signUp()
	token, listID := e.grace()
	a, b, c := e.uploaded(token), e.uploaded(token), e.uploaded(token)
	it := e.addItem(token, listID, "Kettle", map[string]any{"image_ids": imageIDs(a, b, c)})

	got := decode[itemView](t, e.patchImages(token, it.ID, map[string]any{"primary_image_id": c.ID}))
	require.Equal(t, c.ID, got.PrimaryImage.ID)
	require.Equal(t, []string{a.ID, b.ID, c.ID}, got.imageIDs(), "a change of the primary image doesn't change the order")
	require.Zero(t, e.sweep(), "nothing was removed")

	// Together with a new order, the primary image is one of the new images.
	got = decode[itemView](t, e.patchImages(token, it.ID, map[string]any{"image_ids": imageIDs(c, b, a), "primary_image_id": b.ID}))
	require.Equal(t, []string{c.ID, b.ID, a.ID}, got.imageIDs())
	require.Equal(t, b.ID, got.PrimaryImage.ID)

	// null makes the first image the primary image.
	got = decode[itemView](t, e.patchImages(token, it.ID, map[string]any{"primary_image_id": nil}))
	require.Equal(t, c.ID, got.PrimaryImage.ID)
}

func TestWhenThePrimaryImageIsRemovedTheFirstRemainingImageTakesItsPlace(t *testing.T) {
	e := newEnv(t)
	e.signUp()
	token, listID := e.grace()
	a, b, c := e.uploaded(token), e.uploaded(token), e.uploaded(token)
	it := e.addItem(token, listID, "Kettle", map[string]any{"image_ids": imageIDs(a, b, c), "primary_image_id": b.ID})

	got := decode[itemView](t, e.patchImages(token, it.ID, map[string]any{"image_ids": imageIDs(a, c)}))

	require.Equal(t, a.ID, got.PrimaryImage.ID, "the first remaining image")
	require.Equal(t, []string{a.ID, c.ID}, got.imageIDs())
}

func TestAPatchOfImagesIsRefusedForABadPrimaryTooManyImagesAndDuplicates(t *testing.T) {
	e := newEnv(t)
	e.signUp()
	token, listID := e.grace()
	var imgs []imageView
	for range 6 {
		imgs = append(imgs, e.uploaded(token))
	}
	it := e.addItem(token, listID, "Kettle", map[string]any{"image_ids": imageIDs(imgs[0], imgs[1])})
	before := decode[itemView](t, e.getItem(token, it.ID)).imageIDs()

	cases := map[string]struct {
		body map[string]any
		code string
		want int
	}{
		"primary not an image of the item": {map[string]any{"primary_image_id": imgs[2].ID}, "invalid_input", 422},
		"primary not in the new array":     {map[string]any{"image_ids": imageIDs(imgs[0]), "primary_image_id": imgs[1].ID}, "invalid_input", 422},
		"primary not an ID":                {map[string]any{"primary_image_id": "abc"}, "invalid_input", 422},
		"six images":                       {map[string]any{"image_ids": imageIDs(imgs...)}, "too_many_images", 422},
		"a repeated image":                 {map[string]any{"image_ids": []string{imgs[2].ID, imgs[2].ID}}, "invalid_input", 422},
		"null images":                      {map[string]any{"image_ids": nil}, "invalid_input", 422},
		"an image that is not an ID":       {map[string]any{"image_ids": []string{"abc"}}, "reference_not_found", 422},
		"an image that does not exist":     {map[string]any{"image_ids": []string{"9999"}}, "reference_not_found", 422},
	}
	for name, tc := range cases {
		res := e.patchImages(token, it.ID, tc.body)
		require.Equal(t, tc.want, res.StatusCode, name)
		require.Equal(t, tc.code, readProblem(t, res).Code, name)
	}
	require.Equal(t, before, decode[itemView](t, e.getItem(token, it.ID)).imageIDs(), "nothing changed")
	require.Zero(t, e.sweep(), "no image was detached by a refused change")
}

func TestAnImageAnotherResourceHoldsIsA409AndOneTheCallerCantSeeIsA422OnAPatch(t *testing.T) {
	e := newEnv(t)
	e.signUp()
	token, listID := e.grace()
	_, hedy := e.otherUser("Hedy")
	a := e.uploaded(token)
	held := e.uploaded(token)
	it := e.addItem(token, listID, "Kettle", map[string]any{"image_ids": imageIDs(a)})
	e.addItem(token, listID, "Mug", map[string]any{"image_ids": imageIDs(held)})

	res := e.patchImages(token, it.ID, map[string]any{"image_ids": imageIDs(a, held)})
	require.Equal(t, http.StatusConflict, res.StatusCode)
	require.Equal(t, "image_attached", readProblem(t, res).Code)

	res = e.patchImages(token, it.ID, map[string]any{"image_ids": imageIDs(a, e.uploaded(hedy))})
	require.Equal(t, http.StatusUnprocessableEntity, res.StatusCode)
	require.Equal(t, "reference_not_found", readProblem(t, res).Code)
	require.Equal(t, []string{a.ID}, decode[itemView](t, e.getItem(token, it.ID)).imageIDs())
}

func TestAnImageRemovedFromAnItemCanNotBeAttachedAgain(t *testing.T) {
	e := newEnv(t)
	e.signUp()
	token, listID := e.grace()
	a, b := e.uploaded(token), e.uploaded(token)
	it := e.addItem(token, listID, "Kettle", map[string]any{"image_ids": imageIDs(a, b)})
	e.patchImages(token, it.ID, map[string]any{"image_ids": imageIDs(a)})

	res := e.patchImages(token, it.ID, map[string]any{"image_ids": imageIDs(a, b)})

	require.Equal(t, http.StatusUnprocessableEntity, res.StatusCode, "upload it again")
	require.Equal(t, "reference_not_found", readProblem(t, res).Code)
}

func TestOnlyAnEditorChangesAnItemsImages(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	a := e.uploaded(f.sue)
	it := e.addItem(f.mom, f.momList, "Kettle")

	require.Equal(t, http.StatusForbidden, e.patchImages(f.sue, it.ID, map[string]any{"image_ids": imageIDs(a)}).StatusCode)
	require.Equal(t, http.StatusNotFound, e.patchImages(f.hedy, it.ID, map[string]any{"image_ids": imageIDs(a)}).StatusCode)
	require.Empty(t, decode[itemView](t, e.getItem(f.mom, it.ID)).Images)
}

func TestItemImagesAreChangedOnlyThroughImageIDs(t *testing.T) {
	e := newEnv(t)
	e.signUp()
	token, listID := e.grace()
	it := e.addItem(token, listID, "Kettle", map[string]any{"store_links": links("https://a.example/k")})

	for _, key := range []string{"store_links", "images", "primary_image"} {
		res := e.patchItem(token, it.ID, map[string]any{key: nil}, nil)
		require.Equal(t, http.StatusForbidden, res.StatusCode, key)
		require.Equal(t, "field_not_writable", readProblem(t, res).Code, key)
	}
}

func TestTheIfMatchOfAnItemGuardsItsImages(t *testing.T) {
	e := newEnv(t)
	e.signUp()
	token, listID := e.grace()
	a := e.uploaded(token)
	it := e.addItem(token, listID, "Kettle")
	stale := e.getItem(token, it.ID).Header.Get("ETag")
	e.patchItem(token, it.ID, map[string]any{"title": "Mug"}, nil)

	res := e.patchItem(token, it.ID, map[string]any{"image_ids": imageIDs(a)}, http.Header{"If-Match": {stale}})

	require.Equal(t, http.StatusPreconditionFailed, res.StatusCode)
	require.Empty(t, decode[itemView](t, e.getItem(token, it.ID)).Images)
}
