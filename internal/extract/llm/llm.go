// Package llm is stage 3 of the link extraction pipeline. An LLM fills in a
// missing title, price, or image from cleaned page text, and turns free text
// into a title and an optional price range.
//
// Two clients sit behind [Provider]: the native Anthropic Messages API and
// OpenAI-compatible Chat Completions (OpenRouter, OpenAI, Ollama, LiteLLM).
// Only one is active. Both use the standard library HTTP client.
package llm

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// MaxTitleLength is the longest title the LLM may give, in characters. It is
// the longest item title the app accepts.
const MaxTitleLength = 300

// Usage is the token usage of one or more LLM calls. The pipeline exposes it
// as a metric.
type Usage struct {
	InputTokens  int
	OutputTokens int
}

// Add returns the sum of two usages.
func (u Usage) Add(o Usage) Usage {
	return Usage{InputTokens: u.InputTokens + o.InputTokens, OutputTokens: u.OutputTokens + o.OutputTokens}
}

// Completion is the reply to one prompt.
type Completion struct {
	Text  string
	Usage Usage
}

// Image is a picture sent to a model with a prompt.
type Image struct {
	MediaType string // for example "image/jpeg"
	Data      []byte
}

// Provider sends one prompt to a model. Tests use a fake implementation.
type Provider interface {
	Complete(ctx context.Context, system, user string) (Completion, error)
	// CompleteImage is Complete with one image beside the text. A model that
	// can't read images answers with an error.
	CompleteImage(ctx context.Context, system, user string, img Image) (Completion, error)
}

// Config selects and configures the active provider. It mirrors the
// WISHBONE_LLM_* settings.
type Config struct {
	Provider string // "anthropic" or "openai". Empty disables the stage.
	APIKey   string
	BaseURL  string // optional. Defaults to the provider's public API.
	Model    string // optional. Defaults to a small, inexpensive model.
}

const (
	defaultAnthropicURL   = "https://api.anthropic.com"
	defaultAnthropicModel = "claude-haiku-5-5"
	defaultOpenAIURL      = "https://api.openai.com/v1"
	defaultOpenAIModel    = "gpt-4o-mini"

	maxResponseBytes = 1 << 20
	maxOutputTokens  = 512
)

// DefaultTimeout is the request timeout of the default HTTP client.
const DefaultTimeout = 60 * time.Second

// New builds the provider for cfg. It returns (nil, nil) when cfg.Provider is
// empty, which means the stage is disabled. A nil client uses a default with a
// 60-second timeout.
func New(cfg Config, client *http.Client) (Provider, error) {
	if client == nil {
		client = &http.Client{Timeout: DefaultTimeout}
	}
	switch cfg.Provider {
	case "":
		return nil, nil
	case "anthropic":
		if cfg.APIKey == "" {
			return nil, errors.New("llm: anthropic requires an API key")
		}
		return &Anthropic{
			BaseURL: orDefault(cfg.BaseURL, defaultAnthropicURL),
			APIKey:  cfg.APIKey,
			Model:   orDefault(cfg.Model, defaultAnthropicModel),
			Client:  client,
		}, nil
	case "openai":
		return &OpenAI{
			BaseURL: orDefault(cfg.BaseURL, defaultOpenAIURL),
			APIKey:  cfg.APIKey,
			Model:   orDefault(cfg.Model, defaultOpenAIModel),
			Client:  client,
		}, nil
	}
	return nil, fmt.Errorf("llm: unknown provider %q (want anthropic or openai)", cfg.Provider)
}

func orDefault(v, def string) string {
	if v = strings.TrimSpace(v); v != "" {
		return strings.TrimRight(v, "/")
	}
	return def
}

// APIError is a non-2xx reply from a provider.
type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("llm: provider returned HTTP %d: %s", e.Status, e.Message)
}

// post sends a JSON body and returns the response body. A non-2xx status is an
// [*APIError]. The API key never appears in an error.
func post(ctx context.Context, client *http.Client, url string, header map[string]string, body []byte) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("llm: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range header {
		req.Header.Set(k, v)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("llm: request failed: %w", errorWithoutURL(err))
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("llm: read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		msg := strings.TrimSpace(string(data))
		if len(msg) > 300 {
			msg = msg[:300]
		}
		return nil, &APIError{Status: resp.StatusCode, Message: msg}
	}
	return data, nil
}
