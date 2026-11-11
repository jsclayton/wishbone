// Package config holds the runtime configuration for the wishbone server.
//
// Every app-level setting is read from a WISHBONE_* environment variable.
// Standard variables (TZ, OTEL_*) keep their usual names and are read by
// the libraries that own them.
package config

import (
	"errors"
	"fmt"
	"net/url"
	"strings"

	"jsclayton.dev/wishbone/internal/clientip"
)

// Config is embedded into the `serve` command, so Kong fills it from flags
// and environment variables.
type Config struct {
	PublicURL string `name:"public-url" env:"WISHBONE_PUBLIC_URL" required:"" help:"Public base URL, used for login links, QR codes, and passkeys."`
	Port      int    `name:"port" short:"p" env:"WISHBONE_PORT" default:"8080" help:"HTTP port."`
	DataDir   string `name:"data-dir" short:"d" env:"WISHBONE_DATA_DIR" default:"./data" help:"Directory for the SQLite database and images."`

	TrustedProxies []string `name:"trusted-proxies" env:"WISHBONE_TRUSTED_PROXIES" sep:"," help:"CIDRs whose X-Forwarded-For header is trusted."`

	RateLimitRedeem int `name:"rate-limit-redeem" env:"WISHBONE_RATE_LIMIT_REDEEM" default:"10" help:"Login link redemption attempts allowed per minute per client IP."`

	BrightDataAPIKey string `name:"brightdata-api-key" env:"WISHBONE_BRIGHTDATA_API_KEY" help:"Bright Data Web Unlocker API key. Enables extraction stage 2 together with the zone."`
	BrightDataZone   string `name:"brightdata-zone" env:"WISHBONE_BRIGHTDATA_ZONE" help:"Bright Data Web Unlocker zone name."`

	LLMProvider string `name:"llm-provider" env:"WISHBONE_LLM_PROVIDER" help:"LLM extraction provider: anthropic or openai (OpenAI-compatible). Empty disables the LLM stage."`
	LLMAPIKey   string `name:"llm-api-key" env:"WISHBONE_LLM_API_KEY" help:"API key for the LLM provider. Required for anthropic."`
	LLMBaseURL  string `name:"llm-base-url" env:"WISHBONE_LLM_BASE_URL" help:"Base URL for the LLM provider. Defaults to the provider's public API."`
	LLMModel    string `name:"llm-model" env:"WISHBONE_LLM_MODEL" help:"LLM model. Defaults to a small, inexpensive model for the provider. Reasoning models are not supported, because requests set max_tokens and temperature."`

	LogLevel  string `name:"log-level" env:"WISHBONE_LOG_LEVEL" default:"info" enum:"debug,info,warn,error" help:"Log level."`
	LogFormat string `name:"log-format" env:"WISHBONE_LOG_FORMAT" default:"text" enum:"text,json" help:"Log format."`
}

// BrightDataEnabled reports whether the Bright Data stage is configured.
func (c *Config) BrightDataEnabled() bool { return c.BrightDataAPIKey != "" && c.BrightDataZone != "" }

// Validate checks values that Kong cannot check on its own.
func (c *Config) Validate() error {
	u, err := url.Parse(c.PublicURL)
	if err != nil {
		return fmt.Errorf("WISHBONE_PUBLIC_URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return errors.New("WISHBONE_PUBLIC_URL: scheme must be http or https")
	}
	if u.Host == "" {
		return errors.New("WISHBONE_PUBLIC_URL: host is required")
	}
	if _, err := clientip.New(c.TrustedProxies); err != nil {
		return fmt.Errorf("WISHBONE_TRUSTED_PROXIES: %w", err)
	}
	if c.RateLimitRedeem < 1 {
		return errors.New("WISHBONE_RATE_LIMIT_REDEEM: must be at least 1")
	}
	if (c.BrightDataAPIKey == "") != (c.BrightDataZone == "") {
		return errors.New("WISHBONE_BRIGHTDATA_API_KEY and WISHBONE_BRIGHTDATA_ZONE must be set together")
	}
	switch c.LLMProvider {
	case "":
	case "anthropic":
		if c.LLMAPIKey == "" {
			return errors.New("WISHBONE_LLM_API_KEY: required when WISHBONE_LLM_PROVIDER is anthropic")
		}
	case "openai":
	default:
		return errors.New("WISHBONE_LLM_PROVIDER: must be anthropic or openai")
	}
	if c.LLMBaseURL != "" {
		u, err := url.Parse(c.LLMBaseURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return errors.New("WISHBONE_LLM_BASE_URL: must be an http or https URL")
		}
	}
	c.PublicURL = strings.TrimRight(c.PublicURL, "/")
	return nil
}
