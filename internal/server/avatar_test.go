package server

import (
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"jsclayton.dev/wishbone/internal/image"
)

func (e *env) setAvatar(token, userID string, imageID any) *http.Response {
	e.t.Helper()
	return e.patchUser(token, userID, map[string]any{"avatar_image_id": imageID}, nil)
}

// row loads the row of an uploaded image, so a test can look for its file after
// the row is gone.
func (e *env) row(imageID string) image.Image {
	e.t.Helper()
	id, err := strconv.ParseInt(imageID, 10, 64)
	require.NoError(e.t, err)
	img, err := image.Get(e.t.Context(), e.db, id)
	require.NoError(e.t, err)
	return img
}

// hasFile says whether the image store still has the file of an image.
func (e *env) hasFile(img image.Image) bool {
	e.t.Helper()
	f, err := e.images.Open(img)
	if err != nil {
		require.ErrorIs(e.t, err, image.ErrNotFound)
		return false
	}
	_ = f.Close()
	return true
}

func (e *env) imageRows() int {
	e.t.Helper()
	var n int
	require.NoError(e.t, e.db.GetContext(e.t.Context(), &n, `SELECT count(*) FROM images`))
	return n
}

func (e *env) sweep() int {
	e.t.Helper()
	n, err := e.images.Sweep(e.t.Context())
	require.NoError(e.t, err)
	return n
}

func TestAUserWithNoAvatarHasANullAvatar(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()

	raw := decode[map[string]json.RawMessage](t, e.do(request{path: "/users/me", header: cookie(admin)}))

	require.Contains(t, raw, "avatar", "a field with no value is null, not left out")
	require.JSONEq(t, `null`, string(raw["avatar"]))
}

func TestAnUploadedImageBecomesTheAvatarAndNullRemovesIt(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	id, grace := e.otherUser("Grace")
	img := e.uploaded(grace)
	before := e.do(request{path: "/users/" + id, header: cookie(grace)}).Header.Get("ETag")

	res := e.setAvatar(grace, id, img.ID)

	require.Equal(t, http.StatusOK, res.StatusCode)
	u := decode[userView](t, res)
	require.Equal(t, &imageView{ID: img.ID, URL: "/api/v2/images/" + img.ID, Width: 4, Height: 3}, u.Avatar, "an image summary has no content_type")
	require.NotEqual(t, before, res.Header.Get("ETag"), "the avatar is part of the user's version")
	// Anyone who sees Grace sees the same avatar.
	require.Equal(t, u.Avatar, decode[userView](t, e.do(request{path: "/users/" + id, header: cookie(admin)})).Avatar)

	res = e.setAvatar(grace, id, nil)

	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Nil(t, decode[userView](t, res).Avatar)
}

func TestAnAvatarLoadsForEveryoneWhoSeesTheUserAndForNoOneElse(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	graceID, grace := e.otherUser("Grace")
	_, zed := e.otherUser("Zed")
	img := e.uploaded(grace)
	require.Equal(t, http.StatusOK, e.setAvatar(grace, graceID, img.ID).StatusCode)

	require.Equal(t, http.StatusOK, e.getImage(grace, img.ID).StatusCode)
	require.Equal(t, http.StatusOK, e.getImage(admin, img.ID).StatusCode, "the admin sees every user")
	res := e.getImage(zed, img.ID)
	require.Equal(t, http.StatusNotFound, res.StatusCode, "Zed can't see Grace, so can't get her summary")
	require.Equal(t, "not_found", readProblem(t, res).Code)
}

func TestAnImageThatAnotherResourceHoldsIsA409(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	graceID, grace := e.otherUser("Grace")
	zedID, _ := e.otherUser("Zed")
	img := e.uploaded(admin)
	require.Equal(t, http.StatusOK, e.setAvatar(admin, graceID, img.ID).StatusCode)

	res := e.setAvatar(admin, zedID, img.ID)

	require.Equal(t, http.StatusConflict, res.StatusCode)
	require.Equal(t, "image_attached", readProblem(t, res).Code)
	require.Nil(t, decode[userView](t, e.do(request{path: "/users/" + zedID, header: cookie(admin)})).Avatar)
	require.NotNil(t, decode[userView](t, e.do(request{path: "/users/" + graceID, header: cookie(grace)})).Avatar, "Grace keeps hers")
}

func TestSettingTheSameAvatarAgainChangesNothing(t *testing.T) {
	e := newEnv(t)
	e.signUp()
	id, grace := e.otherUser("Grace")
	img := e.uploaded(grace)
	first := e.setAvatar(grace, id, img.ID)
	require.Equal(t, http.StatusOK, first.StatusCode)

	again := e.setAvatar(grace, id, img.ID)

	require.Equal(t, http.StatusOK, again.StatusCode)
	require.Equal(t, first.Header.Get("ETag"), again.Header.Get("ETag"))
	require.Zero(t, e.sweep(), "the image is still held, so the sweep keeps it")
	require.True(t, e.hasFile(e.row(img.ID)))
}

func TestAnImageTheCallerCannotSeeIsA422(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	graceID, grace := e.otherUser("Grace")
	zoeID, _ := e.otherUser("Zoe")
	notGraces := e.uploaded(admin)
	attachedToAnUnseenUser := e.uploaded(admin)
	require.Equal(t, http.StatusOK, e.setAvatar(admin, zoeID, attachedToAnUnseenUser.ID).StatusCode)

	for name, imageID := range map[string]any{
		"an image that doesn't exist":              "999",
		"an ID that is no ID":                      "abc",
		"an empty ID":                              "",
		"an ID with a leading zero":                "01",
		"an image someone else made, not attached": notGraces.ID,
		"an image of a user the caller can't see":  attachedToAnUnseenUser.ID,
	} {
		t.Run(name, func(t *testing.T) {
			res := e.setAvatar(grace, graceID, imageID)
			require.Equal(t, http.StatusUnprocessableEntity, res.StatusCode)
			require.Equal(t, "reference_not_found", readProblem(t, res).Code, "a missing image and a hidden one look the same")
		})
	}

	res := e.setAvatar(grace, graceID, 31)
	require.Equal(t, http.StatusUnprocessableEntity, res.StatusCode, "an ID is a string, not a number")
	require.Equal(t, "invalid_input", readProblem(t, res).Code)
}

func TestAnImageThatWasLetGoCannotBeTakenAgain(t *testing.T) {
	e := newEnv(t)
	e.signUp()
	id, grace := e.otherUser("Grace")
	img := e.uploaded(grace)
	require.Equal(t, http.StatusOK, e.setAvatar(grace, id, img.ID).StatusCode)
	require.Equal(t, http.StatusOK, e.setAvatar(grace, id, nil).StatusCode)

	res := e.setAvatar(grace, id, img.ID)

	require.Equal(t, http.StatusUnprocessableEntity, res.StatusCode)
	require.Equal(t, "reference_not_found", readProblem(t, res).Code)
}

func TestOnlyWhoCanEditAUserSetsTheirAvatar(t *testing.T) {
	e := newEnv(t)
	e.signUp()
	graceID, _ := e.otherUser("Grace")
	_, zed := e.otherUser("Zed")
	img := e.uploaded(zed)

	res := e.setAvatar(zed, graceID, img.ID)

	require.Equal(t, http.StatusNotFound, res.StatusCode, "Zed can't see Grace")
}

func TestTheSweepDeletesAnImageThatIsNoLongerAttached(t *testing.T) {
	e := newEnv(t)
	e.signUp()
	id, grace := e.otherUser("Grace")
	old, current := e.uploaded(grace), e.uploaded(grace)
	require.Equal(t, http.StatusOK, e.setAvatar(grace, id, old.ID).StatusCode)
	require.Equal(t, http.StatusOK, e.setAvatar(grace, id, current.ID).StatusCode, "a new avatar lets go of the old one")

	oldRow := e.row(old.ID)

	require.Equal(t, 1, e.sweep(), "the old avatar, at once, though it is new")

	require.Equal(t, http.StatusNotFound, e.getImage(grace, old.ID).StatusCode)
	require.False(t, e.hasFile(oldRow))
	require.Equal(t, http.StatusOK, e.getImage(grace, current.ID).StatusCode)
	require.True(t, e.hasFile(e.row(current.ID)))

	require.Equal(t, http.StatusOK, e.setAvatar(grace, id, nil).StatusCode)
	require.Equal(t, 1, e.sweep(), "a removed avatar goes too")
	require.Equal(t, 0, e.imageRows())
}

func TestTheSweepKeepsTheFileOfAnImageUntilTheRowIsGone(t *testing.T) {
	e := newEnv(t)
	e.signUp()
	id, grace := e.otherUser("Grace")
	img := e.uploaded(grace)
	require.Equal(t, http.StatusOK, e.setAvatar(grace, id, img.ID).StatusCode)
	row := e.row(img.ID)

	e.clock.advance(30 * 24 * time.Hour)
	require.Zero(t, e.sweep(), "an attached image is never too old")
	require.True(t, e.hasFile(row), "the file of a row that something holds is never deleted")

	require.Equal(t, http.StatusOK, e.setAvatar(grace, id, nil).StatusCode)
	require.Equal(t, 1, e.sweep())
	require.False(t, e.hasFile(row), "after its row went, its file went")
}

func TestTheSweepDeletesAnImageThatWasNotAttachedWithin24Hours(t *testing.T) {
	e := newEnv(t)
	e.signUp()
	_, grace := e.otherUser("Grace")
	img := e.uploaded(grace)
	row := e.row(img.ID)

	e.clock.advance(23*time.Hour + 59*time.Minute)
	require.Zero(t, e.sweep(), "not yet")
	require.Equal(t, http.StatusOK, e.getImage(grace, img.ID).StatusCode)

	e.clock.advance(2 * time.Minute)
	require.Equal(t, 1, e.sweep())
	require.False(t, e.hasFile(row))
	require.Equal(t, http.StatusNotFound, e.getImage(grace, img.ID).StatusCode)
}

// The database refuses to delete a row that a resource still holds, whatever
// the sweep believes.
func TestTheDatabaseRefusesToDeleteAnImageThatIsHeld(t *testing.T) {
	e := newEnv(t)
	e.signUp()
	id, grace := e.otherUser("Grace")
	img := e.uploaded(grace)
	require.Equal(t, http.StatusOK, e.setAvatar(grace, id, img.ID).StatusCode)

	_, err := e.db.ExecContext(t.Context(), `DELETE FROM images`)

	require.Error(t, err)
	require.Equal(t, 1, e.imageRows())
}
