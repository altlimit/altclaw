package provider

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Anthropic implements Provider for the Anthropic Claude API.
type Anthropic struct {
	APIKey string
	Model  string // e.g. "claude-sonnet-4-20250514"
	client *http.Client
}

const anthropicBaseURL = "https://api.anthropic.com/v1"

// NewAnthropic creates a new Anthropic provider.
func NewAnthropic(apiKey, model string) *Anthropic {
	return &Anthropic{
		APIKey: apiKey,
		Model:  model,
		client: NewRelayClient(),
	}
}

func (a *Anthropic) Name() string { return "anthropic" }

// Anthropic API types

type anthropicMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type anthropicRequest struct {
	Model     string             `json:"model"`
	MaxTokens int                `json:"max_tokens"`
	System    string             `json:"system,omitempty"`
	Messages  []anthropicMessage `json:"messages"`
	Stream    bool               `json:"stream,omitempty"`
}

type anthropicContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

type anthropicResponse struct {
	Content []anthropicContentBlock `json:"content"`
	Usage   *struct {
		InputTokens  int64 `json:"input_tokens"`
		OutputTokens int64 `json:"output_tokens"`
	} `json:"usage"`
	Error *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

type anthropicStreamEvent struct {
	Type  string `json:"type"`
	Delta *struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"delta,omitempty"`
	ContentBlock *anthropicContentBlock `json:"content_block,omitempty"`
	Usage *struct {
		InputTokens  int64 `json:"input_tokens"`
		OutputTokens int64 `json:"output_tokens"`
	} `json:"usage,omitempty"`
}

func (a *Anthropic) buildRequest(messages []Message) anthropicRequest {
	var system string
	var msgs []anthropicMessage

	for _, m := range messages {
		if m.Role == "system" {
			system = m.Content
			continue
		}
		var raw json.RawMessage
		if len(m.Files) == 0 {
			raw, _ = json.Marshal(m.Content)
		} else {
			parts := []any{map[string]string{"type": "text", "text": m.Content}}
			for _, f := range m.Files {
				parts = append(parts, map[string]any{
					"type": "image",
					"source": map[string]string{
						"type":       "base64",
						"media_type": f.MimeType,
						"data":       base64.StdEncoding.EncodeToString(f.Data),
					},
				})
			}
			raw, _ = json.Marshal(parts)
		}
		msgs = append(msgs, anthropicMessage{
			Role:    m.Role,
			Content: raw,
		})
	}

	return anthropicRequest{
		Model:     a.Model,
		MaxTokens: 8192,
		System:    system,
		Messages:  msgs,
	}
}

func (a *Anthropic) doRequest(ctx context.Context, body []byte) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, anthropicBaseURL+"/messages", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", a.APIKey)
	req.Header.Set("anthropic-version", "2023-06-01")
	return a.client.Do(req)
}

// Chat sends messages and returns the full response.
func (a *Anthropic) Chat(ctx context.Context, messages []Message) (string, TokenCounts, error) {
	if err := checkProviderRPM(ctx, anthropicBaseURL); err != nil {
		return "", TokenCounts{}, err
	}
	release, err := acquireSem(ctx, anthropicBaseURL)
	if err != nil {
		return "", TokenCounts{}, err
	}
	defer release()
	reqBody := a.buildRequest(messages)
	data, err := json.Marshal(reqBody)
	if err != nil {
		return "", TokenCounts{}, fmt.Errorf("marshal request: %w", err)
	}

	resp, err := a.doRequest(ctx, data)
	if err != nil {
		return "", TokenCounts{}, fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	respData, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", TokenCounts{}, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", TokenCounts{}, fmt.Errorf("anthropic error (status %d): %s", resp.StatusCode, string(respData))
	}

	var result anthropicResponse
	if err := json.Unmarshal(respData, &result); err != nil {
		return "", TokenCounts{}, fmt.Errorf("unmarshal response: %w", err)
	}

	if result.Error != nil {
		return "", TokenCounts{}, fmt.Errorf("anthropic error [%s]: %s", result.Error.Type, result.Error.Message)
	}
	var tc TokenCounts
	if result.Usage != nil {
		tc = TokenCounts{Prompt: result.Usage.InputTokens, Completion: result.Usage.OutputTokens}
	}

	var sb strings.Builder
	for _, block := range result.Content {
		if block.Type == "text" {
			sb.WriteString(block.Text)
		}
	}
	return sb.String(), tc, nil
}

// ChatStream sends messages and streams the response via onChunk callback.
func (a *Anthropic) ChatStream(ctx context.Context, messages []Message, onChunk func(chunk string), onDone func(TokenCounts)) error {
	return withRateAndSem(ctx, anthropicBaseURL, func() error {
	reqBody := a.buildRequest(messages)
	reqBody.Stream = true
	data, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	resp, err := a.doRequest(ctx, data)
	if err != nil {
		return fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respData, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("anthropic error (status %d): %s", resp.StatusCode, string(respData))
	}

	var tc TokenCounts
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		payload := strings.TrimPrefix(line, "data: ")

		var event anthropicStreamEvent
		if err := json.Unmarshal([]byte(payload), &event); err != nil {
			continue
		}

		switch event.Type {
		case "content_block_delta":
			if event.Delta != nil && event.Delta.Text != "" {
				onChunk(event.Delta.Text)
			}
		case "message_start":
			if event.Usage != nil {
				tc.Prompt = event.Usage.InputTokens
			}
		case "message_delta":
			if event.Usage != nil {
				tc.Completion = event.Usage.OutputTokens
			}
		case "message_stop":
			if onDone != nil {
				onDone(tc)
			}
			return nil
		case "error":
			return fmt.Errorf("anthropic stream error: %s", payload)
		}
	}
	if onDone != nil {
		onDone(tc)
	}
	return scanner.Err()
	})
}

// ListModels returns available Claude models.
func (a *Anthropic) ListModels(ctx context.Context) ([]ModelInfo, error) {
	// Anthropic doesn't have a public models endpoint; return known models
	return []ModelInfo{
		{ID: "claude-sonnet-4-20250514", Name: "Claude Sonnet 4", Description: "Best balance of speed and intelligence"},
		{ID: "claude-opus-4-20250514", Name: "Claude Opus 4", Description: "Most capable model"},
		{ID: "claude-haiku-3-5-20241022", Name: "Claude 3.5 Haiku", Description: "Fastest model"},
	}, nil
}
