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

// Ollama implements Provider for local Ollama instances.
type Ollama struct {
	Host   string // e.g. "http://localhost:11434"
	Model  string // e.g. "llama3"
	client *http.Client
}

// NewOllama creates a new Ollama provider.
func NewOllama(host, model string) *Ollama {
	if host == "" {
		host = "http://localhost:11434"
	}
	host = strings.TrimRight(host, "/")
	return &Ollama{
		Host:   host,
		Model:  model,
		client: NewRelayClient(),
	}
}

func (o *Ollama) Name() string { return "ollama" }

type ollamaMessage struct {
	Role    string   `json:"role"`
	Content string   `json:"content"`
	Images  []string `json:"images,omitempty"` // base64 encoded
}

type ollamaChatRequest struct {
	Model    string          `json:"model"`
	Messages []ollamaMessage `json:"messages"`
	Stream   bool            `json:"stream"`
}

type ollamaChatResponse struct {
	Message      ollamaMessage `json:"message"`
	Done         bool          `json:"done"`
	Error        string        `json:"error,omitempty"`
	DoneReason   string        `json:"done_reason,omitempty"`
	PromptEvalCount int64      `json:"prompt_eval_count,omitempty"`
	EvalCount    int64         `json:"eval_count,omitempty"`
}

func toOllamaMessages(msgs []Message) []ollamaMessage {
	out := make([]ollamaMessage, len(msgs))
	for i, m := range msgs {
		out[i] = ollamaMessage{Role: m.Role, Content: m.Content}
		for _, f := range m.Files {
			out[i].Images = append(out[i].Images, base64.StdEncoding.EncodeToString(f.Data))
		}
	}
	return out
}

// Chat sends messages and returns the full response.
func (o *Ollama) Chat(ctx context.Context, messages []Message) (string, TokenCounts, error) {
	if err := checkProviderRPM(ctx, o.Host); err != nil {
		return "", TokenCounts{}, err
	}
	release, err := acquireSem(ctx, o.Host)
	if err != nil {
		return "", TokenCounts{}, err
	}
	defer release()
	body := ollamaChatRequest{
		Model:    o.Model,
		Messages: toOllamaMessages(messages),
		Stream:   false,
	}
	data, err := json.Marshal(body)
	if err != nil {
		return "", TokenCounts{}, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.Host+"/api/chat", bytes.NewReader(data))
	if err != nil {
		return "", TokenCounts{}, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := o.client.Do(req)
	if err != nil {
		return "", TokenCounts{}, fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	respData, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", TokenCounts{}, fmt.Errorf("read response: %w", err)
	}

	var result ollamaChatResponse
	if err := json.Unmarshal(respData, &result); err != nil {
		return "", TokenCounts{}, fmt.Errorf("unmarshal response: %w", err)
	}

	if result.Error != "" {
		return "", TokenCounts{}, fmt.Errorf("ollama error: %s", result.Error)
	}
	tc := TokenCounts{Prompt: result.PromptEvalCount, Completion: result.EvalCount}
	return result.Message.Content, tc, nil
}

// ChatStream sends messages and streams the response via onChunk callback.
func (o *Ollama) ChatStream(ctx context.Context, messages []Message, onChunk func(chunk string), onDone func(TokenCounts)) error {
	return withRateAndSem(ctx, o.Host, func() error {
	body := ollamaChatRequest{
		Model:    o.Model,
		Messages: toOllamaMessages(messages),
		Stream:   true,
	}
	data, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.Host+"/api/chat", bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := o.client.Do(req)
	if err != nil {
		return fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respData, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("ollama error (status %d): %s", resp.StatusCode, string(respData))
	}

	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}

		var chunk ollamaChatResponse
		if err := json.Unmarshal([]byte(line), &chunk); err != nil {
			continue
		}

		if chunk.Message.Content != "" {
			onChunk(chunk.Message.Content)
		}
		if chunk.Done {
			if onDone != nil {
				onDone(TokenCounts{Prompt: chunk.PromptEvalCount, Completion: chunk.EvalCount})
			}
			break
		}
	}
	return scanner.Err()
	})
}

// ListModels returns available models from the Ollama instance.
func (o *Ollama) ListModels(ctx context.Context) ([]ModelInfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, o.Host+"/api/tags", nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
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
		Models []struct {
			Name       string `json:"name"`
			ModifiedAt string `json:"modified_at"`
			Size       int64  `json:"size"`
		} `json:"models"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}

	models := make([]ModelInfo, 0, len(result.Models))
	for _, m := range result.Models {
		models = append(models, ModelInfo{
			ID:   m.Name,
			Name: m.Name,
			Description: fmt.Sprintf("size: %dMB", m.Size/1024/1024),
		})
	}
	return models, nil
}
