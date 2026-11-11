package config

import (
	"os"
	"strings"
	"testing"

	"github.com/alecthomas/kong"
	"github.com/stretchr/testify/require"
)

func TestValidateTrustedProxies(t *testing.T) {
	ok := Config{PublicURL: "http://localhost", RateLimitRedeem: 10, TrustedProxies: []string{"10.0.0.0/8", "fd00::/8"}}
	require.NoError(t, ok.Validate())

	bad := Config{PublicURL: "http://localhost", RateLimitRedeem: 10, TrustedProxies: []string{"10.0.0.0/8", "not-a-cidr"}}
	err := bad.Validate()
	require.ErrorContains(t, err, "WISHBONE_TRUSTED_PROXIES")
}

func TestValidatePublicURL(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{name: "https", in: "https://wish.example.com", want: "https://wish.example.com"},
		{name: "trailing slash trimmed", in: "https://wish.example.com/", want: "https://wish.example.com"},
		{name: "http localhost", in: "http://localhost:8080", want: "http://localhost:8080"},
		{name: "missing scheme", in: "wish.example.com", wantErr: true},
		{name: "wrong scheme", in: "ftp://wish.example.com", wantErr: true},
		{name: "empty", in: "", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := Config{PublicURL: tt.in, RateLimitRedeem: 10}
			err := c.Validate()
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, c.PublicURL)
		})
	}
}

func TestValidateRateLimitRedeem(t *testing.T) {
	for _, n := range []int{1, 10, 1000} {
		c := Config{PublicURL: "http://localhost", RateLimitRedeem: n}
		require.NoError(t, c.Validate(), n)
	}
	for _, n := range []int{0, -1} {
		c := Config{PublicURL: "http://localhost", RateLimitRedeem: n}
		require.ErrorContains(t, c.Validate(), "WISHBONE_RATE_LIMIT_REDEEM", n)
	}
}

func TestBrightDataConfig(t *testing.T) {
	// Kong runs Config.Validate during Parse.
	tryParse := func(t *testing.T, env map[string]string) (Config, error) {
		t.Helper()
		isolateEnv(t)
		for k, v := range env {
			t.Setenv(k, v)
		}
		var c Config
		k, err := kong.New(&c)
		require.NoError(t, err)
		_, err = k.Parse(nil)
		return c, err
	}
	parse := func(t *testing.T, env map[string]string) Config {
		t.Helper()
		c, err := tryParse(t, env)
		require.NoError(t, err)
		return c
	}
	base := map[string]string{"WISHBONE_PUBLIC_URL": "http://localhost"}

	t.Run("unset is disabled", func(t *testing.T) {
		c := parse(t, base)
		require.NoError(t, c.Validate())
		require.False(t, c.BrightDataEnabled())
	})
	t.Run("both set is enabled", func(t *testing.T) {
		c := parse(t, withEnv(base, "WISHBONE_BRIGHTDATA_API_KEY", "k", "WISHBONE_BRIGHTDATA_ZONE", "z"))
		require.NoError(t, c.Validate())
		require.True(t, c.BrightDataEnabled())
		require.Equal(t, "k", c.BrightDataAPIKey)
		require.Equal(t, "z", c.BrightDataZone)
	})
	t.Run("only one set is an error", func(t *testing.T) {
		_, err := tryParse(t, withEnv(base, "WISHBONE_BRIGHTDATA_API_KEY", "k"))
		require.ErrorContains(t, err, "WISHBONE_BRIGHTDATA")
	})

}

// isolateEnv blanks every WISHBONE_* variable in the process environment, so a
// developer's shell or .env file can't change a test. It covers variables added later without changes here.
func isolateEnv(t *testing.T) {
	t.Helper()
	for _, kv := range os.Environ() {
		if k, _, _ := strings.Cut(kv, "="); strings.HasPrefix(k, "WISHBONE_") {
			t.Setenv(k, "") // registers the restore
			require.NoError(t, os.Unsetenv(k))
		}
	}
}

// withEnv returns a copy of base plus the given key/value pairs.
func withEnv(base map[string]string, kv ...string) map[string]string {
	m := map[string]string{}
	for k, v := range base {
		m[k] = v
	}
	for i := 0; i+1 < len(kv); i += 2 {
		m[kv[i]] = kv[i+1]
	}
	return m
}

func TestValidateLLM(t *testing.T) {
	tests := []struct {
		name    string
		c       Config
		wantErr string
	}{
		{name: "disabled", c: Config{}},
		{name: "anthropic", c: Config{LLMProvider: "anthropic", LLMAPIKey: "k"}},
		{name: "anthropic needs key", c: Config{LLMProvider: "anthropic"}, wantErr: "WISHBONE_LLM_API_KEY"},
		{name: "openai without key (Ollama)", c: Config{LLMProvider: "openai", LLMBaseURL: "http://ollama:11434/v1"}},
		{name: "unknown provider", c: Config{LLMProvider: "gemini"}, wantErr: "WISHBONE_LLM_PROVIDER"},
		{name: "bad base URL", c: Config{LLMProvider: "openai", LLMBaseURL: "ollama"}, wantErr: "WISHBONE_LLM_BASE_URL"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.c.PublicURL, tt.c.RateLimitRedeem = "http://localhost", 10
			err := tt.c.Validate()
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, tt.wantErr)
		})
	}
}

func TestLLMConfigFromEnv(t *testing.T) {
	parse := func(t *testing.T, env map[string]string) (Config, error) {
		t.Helper()
		isolateEnv(t)
		for k, v := range env {
			t.Setenv(k, v)
		}
		var c Config
		k, err := kong.New(&c)
		require.NoError(t, err)
		_, err = k.Parse(nil)
		return c, err
	}
	base := map[string]string{"WISHBONE_PUBLIC_URL": "http://localhost"}

	t.Run("unset is disabled", func(t *testing.T) {
		c, err := parse(t, base)
		require.NoError(t, err)
		require.Empty(t, c.LLMProvider)
	})
	t.Run("read from env", func(t *testing.T) {
		c, err := parse(t, withEnv(base, "WISHBONE_LLM_PROVIDER", "openai", "WISHBONE_LLM_BASE_URL", "http://ollama:11434/v1", "WISHBONE_LLM_MODEL", "llama3"))
		require.NoError(t, err)
		require.Equal(t, "openai", c.LLMProvider)
		require.Equal(t, "http://ollama:11434/v1", c.LLMBaseURL)
		require.Equal(t, "llama3", c.LLMModel)
	})
	t.Run("provider name is exact", func(t *testing.T) {
		_, err := parse(t, withEnv(base, "WISHBONE_LLM_PROVIDER", "Anthropic", "WISHBONE_LLM_API_KEY", "k"))
		require.Error(t, err)
	})
	t.Run("anthropic needs a key", func(t *testing.T) {
		_, err := parse(t, withEnv(base, "WISHBONE_LLM_PROVIDER", "anthropic"))
		require.ErrorContains(t, err, "WISHBONE_LLM_API_KEY")
	})
}
