package server

import (
	"context"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"jsclayton.dev/wishbone/internal/setup"
	"jsclayton.dev/wishbone/internal/user"
)

// invalidInput is the 422 for input that failed a rule of the domain. When the
// rule names a field, the problem names it too.
func invalidInput(err error) error {
	detail := &huma.ErrorDetail{Message: err.Error()}
	var fe *user.FieldError
	if errors.As(err, &fe) {
		detail.Location = "body." + fe.Field
	}
	return Error(http.StatusUnprocessableEntity, "invalid_input", err.Error(), detail)
}

type setupStatusOutput struct {
	Body struct {
		IsNeeded bool `json:"is_needed" doc:"True while the instance has no users."`
	}
}

type setupInput struct {
	UserAgent string `header:"User-Agent"`
	Body      struct {
		SetupCode string           `json:"setup_code" minLength:"1" maxLength:"100" doc:"The one-time code in the server log."`
		Name      string           `json:"name" minLength:"1" maxLength:"100"`
		LastName  *string          `json:"last_name,omitempty" required:"false" nullable:"true" maxLength:"100"`
		Birthday  user.PartialDate `json:"birthday"`
	}
}

type setupOutput struct {
	Location  string      `header:"Location"`
	ETag      string      `header:"ETag"`
	SetCookie http.Cookie `header:"Set-Cookie"`
	Body      UserBody
}

func (s *Server) registerSetup(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "get-setup",
		Method:      http.MethodGet,
		Path:        "/setup",
		Summary:     "Whether the instance needs setup",
		Tags:        []string{"Setup"},
	}, func(ctx context.Context, _ *struct{}) (*setupStatusOutput, error) {
		needed, err := s.opts.Setup.IsNeeded(ctx)
		if err != nil {
			return nil, err
		}
		out := &setupStatusOutput{}
		out.Body.IsNeeded = needed
		return out, nil
	})

	huma.Register(api, huma.Operation{
		OperationID:   "post-setup",
		Method:        http.MethodPost,
		Path:          "/setup",
		Summary:       "Create the first user, the admin",
		Description:   "Takes the setup code from the server log. Starts a cookie session. Attempts count against `WISHBONE_RATE_LIMIT_REDEEM`.",
		Tags:          []string{"Setup"},
		DefaultStatus: http.StatusCreated,
		Errors:        []int{http.StatusConflict, http.StatusUnprocessableEntity, http.StatusTooManyRequests},
	}, func(ctx context.Context, in *setupInput) (*setupOutput, error) {
		input := setup.Input{Name: in.Body.Name, Birthday: in.Body.Birthday}
		if in.Body.LastName != nil {
			input.LastName = *in.Body.LastName
		}
		res, err := s.opts.Setup.Complete(ctx, in.Body.SetupCode, input, in.UserAgent)
		switch {
		case errors.Is(err, setup.ErrComplete):
			return nil, Error(http.StatusConflict, "setup_complete", "Setup is done. The instance already has a user.")
		case errors.Is(err, setup.ErrCodeInvalid):
			return nil, Error(http.StatusUnprocessableEntity, "setup_code_invalid", "That setup code is not the one in the server log.",
				&huma.ErrorDetail{Message: "not the setup code", Location: "body.setup_code"})
		case errors.Is(err, user.ErrInvalid):
			return nil, invalidInput(err)
		case err != nil:
			return nil, err
		}
		body := newUserBody(res.User, res.User, true) // a user sees their own primary list
		version, err := versionOf(body)
		if err != nil {
			return nil, err
		}
		return &setupOutput{
			Location:  location("/users/" + formatID(res.User.ID)),
			ETag:      version,
			SetCookie: newSessionCookie(res.Token, res.Session.ExpiresAt),
			Body:      body,
		}, nil
	})
}
