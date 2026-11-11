package main

import (
	"bytes"
	"log/slog"
	"regexp"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"jsclayton.dev/wishbone/internal/setup"
	"jsclayton.dev/wishbone/internal/user"
)

func TestAServerStartWithNoUsersLogsASetupCodeThatWorks(t *testing.T) {
	_, st := newDB(t)
	svc := setup.NewService(st.DB(), time.Now)
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))

	require.NoError(t, announceSetup(t.Context(), svc, logger, "https://wishbone.example"))

	m := regexp.MustCompile(`setup_code=([A-Z0-9-]+)`).FindStringSubmatch(buf.String())
	require.NotNil(t, m, "the log has the code: %s", buf.String())
	require.Contains(t, buf.String(), "https://wishbone.example")
	_, err := svc.Complete(t.Context(), m[1], setup.Input{Name: "Ada", Birthday: user.PartialDate{Month: 12, Day: 10}}, "")
	require.NoError(t, err)
}

func TestAServerStartWithAUserLogsNoSetupCode(t *testing.T) {
	_, st := newDB(t)
	svc := setup.NewService(st.DB(), time.Now)
	code, err := svc.Prepare(t.Context())
	require.NoError(t, err)
	_, err = svc.Complete(t.Context(), code, setup.Input{Name: "Ada", Birthday: user.PartialDate{Month: 12, Day: 10}}, "")
	require.NoError(t, err)
	var buf bytes.Buffer

	require.NoError(t, announceSetup(t.Context(), svc, slog.New(slog.NewTextHandler(&buf, nil)), "https://wishbone.example"))

	require.NotContains(t, buf.String(), "setup_code")
}
