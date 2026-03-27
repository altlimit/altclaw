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

// OpenAI implements Provider for any OpenAI-compatible API.
// Works with OpenAI, Azure OpenAI, Groq, Together, vLLM, LM Studio, etc.
type OpenAI struct {
	BaseURL string // e.g. "https://api.openai.com/v1"
	APIKey  string
	Model   string // e.g. "gpt-4o"
	client  *http.Client
}

// NewOpenAI creates a new OpenAI-compatible provider.
func NewOpenAI(baseURL, apiKey, model string) *OpenAI {
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	baseURL = strings.TrimRight(baseURL, "/")
	return &OpenAI{
		BaseURL: baseURL,
		APIKey:  apiKey,
		Model:   model,
		client:  NewRelayClient(),
	}
}

func (o *OpenAI) Name() string { return "openai" }

type openaiChatMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type openaiRequest struct {
	Model    string              `json:"model"`
	Messages []openaiChatMessage `json:"messages"`
	Stream   bool                `json:"stream,omitempty"`
}

// toOpenAIMessages converts provider Messages to OpenAI multipart format.
func toOpenAIMessages(msgs []Message) []openaiChatMessage {
	out := make([]openaiChatMessage, 0, len(msgs))
	for _, m := range msgs {
		if len(m.Files) == 0 {
			// Simple text message
			raw, _ := json.Marshal(m.Content)
			out = append(out, openaiChatMessage{Role: m.Role, Content: raw})
		} else {
			// Multipart content array
			parts := []any{map[string]string{"type": "text", "text": m.Content}}
			for _, f := range m.Files {
				dataURI := "data:" + f.MimeType + ";base64," + base64.StdEncoding.EncodeToString(f.Data)
				parts = append(parts, map[string]any{
					"type": "image_url",
					"image_url": map[string]string{"url": dataURI},
				})
			}
			raw, _ := json.Marshal(parts)
			out = append(out, openaiChatMessage{Role: m.Role, Content: raw})
		}
	}
	return out
}

type openaiChoice struct {
	Message Message `json:"message"`
	Delta   struct {
		Content string `json:"content"`
	} `json:"delta"`
	FinishReason *string `json:"finish_reason"`
}

type openaiResponse struct {
	Choices []openaiChoice `json:"choices"`
	Usage   *struct {
		PromptTokens     int64 `json:"prompt_tokens"`
		CompletionTokens int64 `json:"completion_tokens"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// Chat sends messages and returns the full response.
func (o *OpenAI) Chat(ctx context.Context, messages []Message) (string, TokenCounts, error) {
	if err := checkProviderRPM(ctx, o.BaseURL); err != nil {
		return "", TokenCounts{}, err
	}
	release, err := acquireSem(ctx, o.BaseURL)
	if err != nil {
		return "", TokenCounts{}, err
	}
	defer release()
	body := openaiRequest{
		Model:    o.Model,
		Messages: toOpenAIMessages(messages),
	}
	data, err := json.Marshal(body)
	if err != nil {
		return "", TokenCounts{}, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.BaseURL+"/chat/completions", bytes.NewReader(data))
	if err != nil {
		return "", TokenCounts{}, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if o.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+o.APIKey)
	}

	resp, err := o.client.Do(req)
	if err != nil {
		return "", TokenCounts{}, fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	respData, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", TokenCounts{}, fmt.Errorf("read response: %w", err)
	}

	var result openaiResponse
	if err := json.Unmarshal(respData, &result); err != nil {
		return "", TokenCounts{}, fmt.Errorf("unmarshal response: %w", err)
	}

	if result.Error != nil {
		return "", TokenCounts{}, fmt.Errorf("api error: %s", result.Error.Message)
	}
	if len(result.Choices) == 0 {
		return "", TokenCounts{}, fmt.Errorf("no choices in response")
	}
	var tc TokenCounts
	if result.Usage != nil {
		tc = TokenCounts{Prompt: result.Usage.PromptTokens, Completion: result.Usage.CompletionTokens}
	}
	return result.Choices[0].Message.Content, tc, nil
}

// ChatStream sends messages and streams the response via onChunk callback.
func (o *OpenAI) ChatStream(ctx context.Context, messages []Message, onChunk func(chunk string), onDone func(TokenCounts)) error {
	return withRateAndSem(ctx, o.BaseURL, func() error {
	body := openaiRequest{
		Model:    o.Model,
		Messages: toOpenAIMessages(messages),
		Stream:   true,
	}
	data, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.BaseURL+"/chat/completions", bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if o.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+o.APIKey)
	}

	resp, err := o.client.Do(req)
	if err != nil {
		return fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respData, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("api error (status %d): %s", resp.StatusCode, string(respData))
	}

	var tc TokenCounts
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		payload := strings.TrimPrefix(line, "data: ")
		if payload == "[DONE]" {
			break
		}

		var chunk openaiResponse
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			continue // skip malformed chunks
		}
		if len(chunk.Choices) > 0 && chunk.Choices[0].Delta.Content != "" {
			onChunk(chunk.Choices[0].Delta.Content)
		}
		if chunk.Usage != nil {
			tc = TokenCounts{Prompt: chunk.Usage.PromptTokens, Completion: chunk.Usage.CompletionTokens}
		}
	}
	if onDone != nil {
		onDone(tc)
	}
	return scanner.Err()
	})
}

// ListModels returns available models from the OpenAI-compatible API.
func (o *OpenAI) ListModels(ctx context.Context) ([]ModelInfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, o.BaseURL+"/models", nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	if o.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+o.APIKey)
	}

	resp, err := o.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	var result struct {
		Data []struct {
			ID      string `json:"id"`
			OwnedBy string `json:"owned_by"`
		} `json:"data"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}
	if result.Error != nil {
		return nil, fmt.Errorf("api error: %s", result.Error.Message)
	}

	models := make([]ModelInfo, 0, len(result.Data))
	for _, m := range result.Data {
		models = append(models, ModelInfo{
			ID:   m.ID,
			Name: m.ID,
			Description: "by " + m.OwnedBy,
		})
	}
	return models, nil
}
