package llm

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
)

// OpenAI is a client for Chat Completions on any compatible server.
type OpenAI struct {
	BaseURL string // for example https://api.openai.com/v1
	APIKey  string // optional, for example Ollama needs none
	Model   string
	Client  *http.Client
}

// Complete implements [Provider].
func (o *OpenAI) Complete(ctx context.Context, system, user string) (Completion, error) {
	return o.send(ctx, system, user)
}

// CompleteImage implements [Provider]. The image is sent as a data URL.
func (o *OpenAI) CompleteImage(ctx context.Context, system, user string, img Image) (Completion, error) {
	return o.send(ctx, system, []map[string]any{
		{"type": "text", "text": user},
		{"type": "image_url", "image_url": map[string]any{
			"url": "data:" + img.MediaType + ";base64," + base64.StdEncoding.EncodeToString(img.Data),
		}},
	})
}

// send posts one user turn. content is a string or a list of content parts.
func (o *OpenAI) send(ctx context.Context, system string, content any) (Completion, error) {
	body, err := json.Marshal(map[string]any{
		"model":       o.Model,
		"max_tokens":  maxOutputTokens,
		"temperature": 0,
		"messages": []map[string]any{
			{"role": "system", "content": system},
			{"role": "user", "content": content},
		},
	})
	if err != nil {
		return Completion{}, err
	}
	header := map[string]string{}
	if o.APIKey != "" {
		header["Authorization"] = "Bearer " + o.APIKey
	}
	data, err := post(ctx, o.Client, o.BaseURL+"/chat/completions", header, body)
	if err != nil {
		return Completion{}, err
	}
	var r struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(data, &r); err != nil {
		return Completion{}, errors.New("llm: openai returned invalid JSON")
	}
	usage := Usage{InputTokens: r.Usage.PromptTokens, OutputTokens: r.Usage.CompletionTokens}
	if len(r.Choices) == 0 || r.Choices[0].Message.Content == "" {
		return Completion{Usage: usage}, errors.New("llm: openai returned no text")
	}
	return Completion{
		Text:  r.Choices[0].Message.Content,
		Usage: usage,
	}, nil
}
