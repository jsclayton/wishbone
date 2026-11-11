package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"mime"
	"net/http"
	"reflect"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/jmoiron/sqlx"

	"jsclayton.dev/wishbone/internal/gift"
	"jsclayton.dev/wishbone/internal/image"
	"jsclayton.dev/wishbone/internal/imagestore"
	"jsclayton.dev/wishbone/internal/item"
	"jsclayton.dev/wishbone/internal/list"
	"jsclayton.dev/wishbone/internal/user"
)

// imageCacheControl is what the bytes of an image are served with. An image
// never changes, so a browser keeps it for a year. It is private because
// the image follows its resource's visibility (docs/spec.md, API principles,
// Images).
const imageCacheControl = "private, max-age=31536000, immutable"

// imageTooLargeLimit is the number of bytes at which huma stops reading an
// upload and answers 413. It is one byte past the limit, so a body of exactly
// [imagestore.MaxBytes] still passes.
const imageTooLargeLimit = imagestore.MaxBytes + 1

// ImageSummary is an image as another resource contains it: a user's avatar
// (docs/routes.md, Summary shapes). It has no `can_*` fields, because a
// client can't do anything to an image.
type ImageSummary struct {
	ID     string `json:"id" example:"31" doc:"An opaque string. Don't parse it."`
	URL    string `json:"url" example:"/api/v2/images/31" doc:"The address of the bytes. A GET with the same sign-in as every other route returns them."`
	Width  int    `json:"width" doc:"Pixels."`
	Height int    `json:"height" doc:"Pixels."`
}

// OptionalImage is an image summary, or null when there is none: the type of a
// field such as a user's `avatar`. huma can't mark an object nullable on a
// field, so the type does it.
type OptionalImage struct{ *ImageSummary }

// MarshalJSON writes null for no image.
func (o OptionalImage) MarshalJSON() ([]byte, error) {
	if o.ImageSummary == nil {
		return []byte("null"), nil
	}
	return json.Marshal(o.ImageSummary)
}

// Schema is the schema of an image summary that can also be null.
func (OptionalImage) Schema(r huma.Registry) *huma.Schema {
	s := *r.Schema(reflect.TypeFor[ImageSummary](), false, "")
	s.Nullable = true
	return &s
}

func optionalImage(s *image.Summary) OptionalImage {
	if s == nil {
		return OptionalImage{}
	}
	summary := newImageSummary(*s)
	return OptionalImage{&summary}
}

// ImageBody is the image that `POST /images` returns.
type ImageBody struct {
	ImageSummary
	ContentType string `json:"content_type" example:"image/jpeg" doc:"The type of the bytes at url. Always image/jpeg."`
}

func imageURL(id int64) string { return apiPrefix + "/images/" + formatID(id) }

func newImageSummary(s image.Summary) ImageSummary {
	return ImageSummary{ID: formatID(s.ID), URL: imageURL(s.ID), Width: s.Width, Height: s.Height}
}

func newImageBody(img image.Image) ImageBody {
	return ImageBody{
		ImageSummary: newImageSummary(image.Summary{ID: img.ID, Width: img.Width, Height: img.Height}),
		ContentType:  imagestore.ContentType,
	}
}

// userSight says whether the caller gets the summary of the user with an ID,
// which is what the visibility of an avatar follows (see [canSeeAvatarOf]). The
// summary of a user reaches the caller in two ways: as a user, a parent or a
// child of a user the caller can see (user.CanSeeSummary), and on a list the
// caller can see, as a recipient or as the user who added an item
// (list.SummaryReaches). A route that has a transaction open passes a reader of
// the transaction (see [sightIn]), and one that has not passes the services (see
// [Server.sight]).
type userSight func(ctx context.Context, owner int64) (bool, error)

// itemSight says whether the caller can see the item with an ID, which is what
// the visibility of an item's image follows.
type itemSight func(ctx context.Context, itemID int64) (bool, error)

// giftSight says whether the caller can see the gift with an ID, which is what
// the visibility of the image that a gift keeps follows.
type giftSight func(ctx context.Context, giftID int64) (bool, error)

// sight is what a caller sees, for the rules of who may load an image.
type sight struct {
	user userSight
	item itemSight
	gift giftSight
}

// sightIn is the [sight] of a caller, read through q: a database, or the
// transaction of a write.
func sightIn(q sqlx.QueryerContext, caller user.User) sight {
	return sight{
		user: func(ctx context.Context, owner int64) (bool, error) {
			if ok, err := user.CanSeeSummary(ctx, q, caller, owner); err != nil || ok {
				return ok, err
			}
			if ok, err := list.SummaryReaches(ctx, q, caller, owner); err != nil || ok {
				return ok, err
			}
			return gift.SummaryReaches(ctx, q, caller, owner)
		},
		item: func(ctx context.Context, itemID int64) (bool, error) { return item.CanSee(ctx, q, caller, itemID) },
		gift: func(ctx context.Context, giftID int64) (bool, error) { return gift.CanSee(ctx, q, caller, giftID) },
	}
}

// sight is the [sight] of a caller, read through the database.
func (s *Server) sight(caller user.User) sight {
	return sight{
		user: func(ctx context.Context, owner int64) (bool, error) {
			if ok, err := s.opts.Users.CanSeeSummary(ctx, caller, owner); err != nil || ok {
				return ok, err
			}
			if ok, err := s.opts.Lists.SummaryReaches(ctx, caller, owner); err != nil || ok {
				return ok, err
			}
			return s.opts.Gifts.SummaryReaches(ctx, caller, owner)
		},
		item: func(ctx context.Context, itemID int64) (bool, error) { return s.opts.Items.CanSee(ctx, caller, itemID) },
		gift: func(ctx context.Context, giftID int64) (bool, error) { return s.opts.Gifts.CanSee(ctx, caller, giftID) },
	}
}

// imageVisible says whether the caller may see an image, given what holds it.
// An image that nothing holds is the uploader's alone. A held image follows the
// resource that holds it.
func imageVisible(ctx context.Context, sees sight, caller user.User, img image.Image, held image.Attachment) (bool, error) {
	switch held.Kind {
	case image.KindAvatar:
		return canSeeAvatarOf(ctx, sees.user, held.ID)
	case image.KindItem:
		return sees.item(ctx, held.ID)
	case image.KindGift:
		return sees.gift(ctx, held.ID)
	}
	// A later kind of holder adds its rule above.
	return img.UploadedBy != nil && caller.ID == *img.UploadedBy, nil
}

// canSeeAvatarOf says whether the caller may load the avatar of a user: the
// avatar is visible to everyone who gets that user's summary. A user's summary
// reaches the users who can see the user, and the users who can see a parent or
// a child of the user (user.CanSeeSummary, next to the rule of who can see a
// user), and the users who can see a list that has the user as a recipient or
// as the adder of an item (list.SummaryReaches, next to the rule of who can see
// a list). See [userSight].
func canSeeAvatarOf(ctx context.Context, sees userSight, owner int64) (bool, error) {
	return sees(ctx, owner)
}

// imageProblem turns an error of the image package into a problem with a code.
func imageProblem(err error) error {
	switch {
	case errors.Is(err, image.ErrNotFound):
		return notFound()
	case errors.Is(err, image.ErrFetchFailed):
		return Error(http.StatusUnprocessableEntity, "image_fetch_failed", "That image couldn't be downloaded. Use another one, or add the item without it.")
	case errors.Is(err, image.ErrInvalid):
		// The store words its messages for a log, in lower case with no stop.
		detail := strings.TrimSuffix(err.Error(), ": "+imagestore.ErrInvalid.Error())
		detail = strings.ToUpper(detail[:1]) + detail[1:] + "."
		return Error(http.StatusUnprocessableEntity, "image_invalid", detail)
	}
	return err
}

// referenceNotFound is the 422 for a body field that names a resource the
// caller can't see. A missing resource and a hidden one look the same.
func referenceNotFound(location, detail string) error {
	return Error(http.StatusUnprocessableEntity, "reference_not_found", detail,
		&huma.ErrorDetail{Location: location, Message: "names a resource that the caller can't see"})
}

// attachableImage checks that the caller may attach an image to a resource, in
// the transaction of the write. holder says whether the resource already holds
// the image, which makes the attach a change of nothing. It is a 422
// reference_not_found for an image the caller can't see (or that is gone), and a
// 409 image_attached for a visible image that another resource holds.
func attachableImage(ctx context.Context, q sqlx.ExtContext, caller user.User, imageID int64, location string, holder func(image.Attachment) bool) error {
	notThere := func() error { return referenceNotFound(location, "That image doesn't exist, or you can't see it.") }
	img, err := image.Get(ctx, q, imageID)
	if errors.Is(err, image.ErrNotFound) {
		return notThere()
	}
	if err != nil {
		return err
	}
	held, err := image.AttachedTo(ctx, q, imageID)
	if err != nil {
		return err
	}
	if img.Detached {
		return notThere()
	}
	visible, err := imageVisible(ctx, sightIn(q, caller), caller, img, held)
	if err != nil {
		return err
	}
	if !visible {
		return notThere()
	}
	if held.Attached() && !holder(held) {
		return Error(http.StatusConflict, "image_attached", "That image is already attached to something else. Upload it again to use it here.")
	}
	return nil
}

type createImageInput struct {
	RawBody     []byte
	contentType string
}

// Resolve reads the type of the body. It is not a header parameter, which would
// put a Content-Type parameter in the document.
func (i *createImageInput) Resolve(ctx huma.Context) []error {
	i.contentType = ctx.Header("Content-Type")
	return nil
}

// downloadRequest is the JSON body of POST /images that names an address.
type downloadRequest struct {
	URL string `json:"url" format:"uri" doc:"An http or https address of an image."`
}

// downloadAddress reads the address from the JSON body of a download.
func downloadAddress(raw []byte) (string, error) {
	var in struct {
		URL any `json:"url"`
	}
	bad := Error(http.StatusUnprocessableEntity, "invalid_input", "Send {\"url\": ...} with the address of an image.",
		&huma.ErrorDetail{Location: "body.url", Message: "is required, and is a string"})
	if err := json.Unmarshal(raw, &in); err != nil {
		return "", bad
	}
	url, ok := in.URL.(string)
	if !ok || strings.TrimSpace(url) == "" {
		return "", bad
	}
	return strings.TrimSpace(url), nil
}

type imageInput struct {
	ID string `path:"id" example:"31"`
}

var imageResponses = map[string]*huma.Response{
	"200": {
		Description: "The bytes of the image: a JPEG.",
		Headers:     map[string]*huma.Param{"Cache-Control": {Description: imageCacheControl, Schema: &huma.Schema{Type: "string"}}},
		Content:     map[string]*huma.MediaType{imagestore.ContentType: {Schema: &huma.Schema{Type: "string", Format: "binary"}}},
	},
}

func (s *Server) registerImages(api huma.API) {
	binary := &huma.MediaType{Schema: &huma.Schema{Type: "string", Format: "binary"}}
	huma.Register(api, huma.Operation{
		OperationID:   "create-image",
		Method:        http.MethodPost,
		Path:          "/images",
		Summary:       "Upload an image",
		Description:   "The body is the bytes of a JPEG, PNG, GIF, or WebP file, at most 10 MiB, or `application/json` with `{\"url\"}`, which the server downloads. The server decodes the file and stores a JPEG of at most 1200 px on its longest side, with no EXIF or GPS metadata, whatever the `Content-Type` says. A download refuses the instance's own network, and any failure of it (the address is on that network, the download fails or is too large, or the file is not an image) is a 422 `image_fetch_failed`. A download doesn't count against the hourly limit. The image is attached to nothing: only the uploader sees it until a client attaches it by its ID (for example with `avatar_image_id`), and the server deletes it 24 hours after the upload if nothing holds it.",
		Tags:          []string{"Images"},
		DefaultStatus: http.StatusCreated,
		MaxBodyBytes:  imageTooLargeLimit,
		RequestBody: &huma.RequestBody{Content: map[string]*huma.MediaType{
			"image/jpeg": binary, "image/png": binary, "image/gif": binary, "image/webp": binary,
		}},
		Errors: []int{http.StatusUnauthorized, http.StatusRequestEntityTooLarge, http.StatusUnprocessableEntity},
	}, func(ctx context.Context, in *createImageInput) (*createdOutput[ImageBody], error) {
		caller, _ := CallerFrom(ctx)
		var img image.Image
		var err error
		if mediaType, _, _ := mime.ParseMediaType(in.contentType); mediaType == "application/json" {
			var address string
			if address, err = downloadAddress(in.RawBody); err != nil {
				return nil, err
			}
			img, err = s.opts.Images.CreateFromURL(ctx, caller.User.ID, address)
		} else {
			img, err = s.opts.Images.Create(ctx, caller.User.ID, bytes.NewReader(in.RawBody))
		}
		if err != nil {
			return nil, imageProblem(err)
		}
		return createdOf("/images/"+formatID(img.ID), newImageBody(img))
	})
	// huma marks a raw body as required, and answers an empty one with a 400.
	// An empty body is not an image, so it is a 422 image_invalid like any other
	// bytes that are not an image.
	post := api.OpenAPI().Paths[apiPrefix+"/images"].Post
	post.RequestBody.Required = false
	post.RequestBody.Content["application/json"] = &huma.MediaType{
		Schema: api.OpenAPI().Components.Schemas.Schema(reflect.TypeFor[downloadRequest](), true, "DownloadImageRequest"),
	}

	huma.Register(api, huma.Operation{
		OperationID: "get-image",
		Method:      http.MethodGet,
		Path:        "/images/{id}",
		Summary:     "The bytes of an image",
		Description: "Before the image is attached, only the user who uploaded it can see it. After that, it follows the visibility of the resource it is attached to, and an avatar is also visible to everyone who gets that user's summary. An image that a resource let go of is a 404 for everyone, its uploader too. Every other caller gets a 404.",
		Tags:        []string{"Images"},
		Errors:      []int{http.StatusUnauthorized, http.StatusNotFound},
		Responses:   imageResponses,
	}, func(ctx context.Context, in *imageInput) (*huma.StreamResponse, error) {
		caller, _ := CallerFrom(ctx)
		id, ok := parseID(in.ID)
		if !ok {
			return nil, notFound()
		}
		img, err := s.opts.Images.Get(ctx, id)
		if err != nil {
			return nil, imageProblem(err)
		}
		// A detached image is gone for everyone, its uploader too: the server deletes
		// it, and it can't be attached again. Otherwise the uploader of an image of a
		// deleted item could tell from a 200 or a 404 whether the item had gifts.
		if img.Detached {
			return nil, notFound()
		}
		held, err := s.opts.Images.Attachment(ctx, id)
		if err != nil {
			return nil, err
		}
		visible, err := imageVisible(ctx, s.sight(caller.User), caller.User, img, held)
		if err != nil {
			return nil, err
		}
		if !visible {
			return nil, notFound()
		}
		f, err := s.opts.Images.Open(img)
		if err != nil {
			return nil, imageProblem(err)
		}
		return &huma.StreamResponse{Body: func(hctx huma.Context) {
			defer func() { _ = f.Close() }()
			r, w := humachi.Unwrap(hctx)
			w.Header().Set("Content-Type", imagestore.ContentType)
			w.Header().Set("Cache-Control", imageCacheControl)
			// ServeContent answers Range requests.
			http.ServeContent(w, r, "", time.Time{}, f)
		}}, nil
	})
}
