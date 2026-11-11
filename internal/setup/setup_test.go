package setup_test

import (
	"path/filepath"
	"regexp"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/require"

	"jsclayton.dev/wishbone/internal/setup"
	"jsclayton.dev/wishbone/internal/store"
	"jsclayton.dev/wishbone/internal/user"
)

var now = time.Date(2026, time.November, 8, 12, 0, 0, 0, time.UTC)

func newService(t *testing.T) (*setup.Service, *sqlx.DB) {
	t.Helper()
	st, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "wishbone.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	require.NoError(t, st.Migrate(t.Context()))
	return setup.NewService(st.DB(), func() time.Time { return now }), st.DB()
}

var ada = setup.Input{Name: "Ada", Birthday: user.PartialDate{Month: 12, Day: 10}}

func TestAFreshInstanceNeedsSetup(t *testing.T) {
	svc, _ := newService(t)
	needed, err := svc.IsNeeded(t.Context())
	require.NoError(t, err)
	require.True(t, needed)
}

func TestPrepareMakesAReadableCodeAndStoresOnlyItsHash(t *testing.T) {
	svc, db := newService(t)

	code, err := svc.Prepare(t.Context())
	require.NoError(t, err)
	require.Regexp(t, regexp.MustCompile(`^[A-Z2-9]{4}-[A-Z2-9]{4}-[A-Z2-9]{4}$`), code)

	var stored []string
	require.NoError(t, db.SelectContext(t.Context(), &stored, `SELECT code_hash FROM setup_codes`))
	require.Len(t, stored, 1)
	require.NotContains(t, stored[0], code)
}

func TestCompleteCreatesTheAdminAndASessionAndUsesUpTheCode(t *testing.T) {
	svc, db := newService(t)
	code, err := svc.Prepare(t.Context())
	require.NoError(t, err)

	res, err := svc.Complete(t.Context(), code, ada, "test agent")
	require.NoError(t, err)
	require.True(t, res.User.IsAdmin)
	require.Equal(t, "Ada", res.User.Name)
	require.NotEmpty(t, res.Token)

	var codes, sessions int
	require.NoError(t, db.GetContext(t.Context(), &codes, `SELECT count(*) FROM setup_codes`))
	require.NoError(t, db.GetContext(t.Context(), &sessions, `SELECT count(*) FROM sessions`))
	require.Zero(t, codes)
	require.Equal(t, 1, sessions)

	needed, err := svc.IsNeeded(t.Context())
	require.NoError(t, err)
	require.False(t, needed)
}

func TestTheCodeIsReadWithoutRegardToCaseDashesOrSpaces(t *testing.T) {
	svc, _ := newService(t)
	code, err := svc.Prepare(t.Context())
	require.NoError(t, err)

	typed := " " + regexp.MustCompile(`-`).ReplaceAllString(code, " ")
	_, err = svc.Complete(t.Context(), toLower(typed), ada, "")
	require.NoError(t, err)
}

func toLower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 'a' - 'A'
		}
	}
	return string(b)
}

func TestAWrongCodeCreatesNothingAndKeepsTheRealCode(t *testing.T) {
	svc, _ := newService(t)
	code, err := svc.Prepare(t.Context())
	require.NoError(t, err)

	_, err = svc.Complete(t.Context(), "AAAA-AAAA-AAAA", ada, "")
	require.ErrorIs(t, err, setup.ErrCodeInvalid)

	needed, err := svc.IsNeeded(t.Context())
	require.NoError(t, err)
	require.True(t, needed)
	_, err = svc.Complete(t.Context(), code, ada, "")
	require.NoError(t, err, "a wrong guess does not use up the code")
}

func TestAnInstanceWithAUserIsComplete(t *testing.T) {
	svc, _ := newService(t)
	code, err := svc.Prepare(t.Context())
	require.NoError(t, err)
	_, err = svc.Complete(t.Context(), code, ada, "")
	require.NoError(t, err)

	_, err = svc.Complete(t.Context(), code, ada, "")
	require.ErrorIs(t, err, setup.ErrComplete, "the right code does not matter any more")
	_, err = svc.Complete(t.Context(), "AAAA-AAAA-AAAA", ada, "")
	require.ErrorIs(t, err, setup.ErrComplete)

	again, err := svc.Prepare(t.Context())
	require.NoError(t, err)
	require.Empty(t, again, "a server start with users makes no code")
}

func TestAServerStartReplacesTheCode(t *testing.T) {
	svc, _ := newService(t)
	first, err := svc.Prepare(t.Context())
	require.NoError(t, err)
	second, err := svc.Prepare(t.Context())
	require.NoError(t, err)
	require.NotEqual(t, first, second)

	_, err = svc.Complete(t.Context(), first, ada, "")
	require.ErrorIs(t, err, setup.ErrCodeInvalid)
	_, err = svc.Complete(t.Context(), second, ada, "")
	require.NoError(t, err)
}

func TestInvalidInputUsesUpNothing(t *testing.T) {
	svc, _ := newService(t)
	code, err := svc.Prepare(t.Context())
	require.NoError(t, err)

	_, err = svc.Complete(t.Context(), code, setup.Input{Name: "", Birthday: ada.Birthday}, "")
	require.ErrorIs(t, err, user.ErrInvalid)
	_, err = svc.Complete(t.Context(), code, ada, "")
	require.NoError(t, err)
}
