package llm

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
)

// errorWithoutURL unwraps a *url.Error so the message doesn't repeat the URL.
func errorWithoutURL(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}

// Anthropic is a client for the native Messages API.
type Anthropic struct {
	BaseURL string
	APIKey  string
	Model   string
	Client  *http.Client
}

// Complete implements [Provider].
func (a *Anthropic) Complete(ctx context.Context, system, user string) (Completion, error) {
	return a.send(ctx, system, user)
}

// CompleteImage implements [Provider]. The image goes before the text, as
// Anthropic advises.
func (a *Anthropic) CompleteImage(ctx context.Context, system, user string, img Image) (Completion, error) {
	return a.send(ctx, system, []map[string]any{
		{"type": "image", "source": map[string]any{
			"type":       "base64",
			"media_type": img.MediaType,
			"data":       base64.StdEncoding.EncodeToString(img.Data),
		}},
		{"type": "text", "text": user},
	})
}

// send posts one user turn. content is a string or a list of content blocks.
func (a *Anthropic) send(ctx context.Context, system string, content any) (Completion, error) {
	body, err := json.Marshal(map[string]any{
		"model":       a.Model,
		"max_tokens":  maxOutputTokens,
		"temperature": 0,
		"system":      system,
		"messages":    []map[string]any{{"role": "user", "content": content}},
	})
	if err != nil {
		return Completion{}, err
	}
	data, err := post(ctx, a.Client, a.BaseURL+"/v1/messages", map[string]string{
		"x-api-key":         a.APIKey,
		"anthropic-version": "2023-06-01",
	}, body)
	if err != nil {
		return Completion{}, err
	}
	var r struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(data, &r); err != nil {
		return Completion{}, errors.New("llm: anthropic returned invalid JSON")
	}
	var text strings.Builder
	for _, c := range r.Content {
		if c.Type == "text" {
			text.WriteString(c.Text)
		}
	}
	usage := Usage{InputTokens: r.Usage.InputTokens, OutputTokens: r.Usage.OutputTokens}
	if text.Len() == 0 {
		return Completion{Usage: usage}, errors.New("llm: anthropic returned no text")
	}
	return Completion{
		Text:  text.String(),
		Usage: usage,
	}, nil
}
