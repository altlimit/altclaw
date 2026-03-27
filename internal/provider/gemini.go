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
	"net/url"
	"strings"
)

// Gemini implements Provider for the Google Gemini API.
type Gemini struct {
	APIKey string
	Model  string // e.g. "gemini-2.5-flash"
	client *http.Client
}

const geminiBaseURL = "https://generativelanguage.googleapis.com/v1beta"

// NewGemini creates a new Gemini provider.
func NewGemini(apiKey, model string) *Gemini {
	return &Gemini{
		APIKey: apiKey,
		Model:  model,
		client: NewRelayClient(),
	}
}

func (g *Gemini) Name() string { return "gemini" }

// Gemini API types

type geminiPart struct {
	Text       string          `json:"text,omitempty"`
	InlineData *geminiInline   `json:"inlineData,omitempty"`
}

type geminiInline struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"` // base64
}

type geminiContent struct {
	Role  string       `json:"role"`
	Parts []geminiPart `json:"parts"`
}

type geminiRequest struct {
	Contents         []geminiContent  `json:"contents"`
	SystemInstruction *geminiContent  `json:"systemInstruction,omitempty"`
}

type geminiCandidate struct {
	Content geminiContent `json:"content"`
}

type geminiResponse struct {
	Candidates []geminiCandidate `json:"candidates"`
	UsageMetadata *struct {
		PromptTokenCount     int64 `json:"promptTokenCount"`
		CandidatesTokenCount int64 `json:"candidatesTokenCount"`
	} `json:"usageMetadata"`
	Error *struct {
		Message string `json:"message"`
		Code    int    `json:"code"`
	} `json:"error"`
}

type geminiModelList struct {
	Models []struct {
		Name        string `json:"name"`
		DisplayName string `json:"displayName"`
		Description string `json:"description"`
	} `json:"models"`
}

// toGeminiContents converts provider Messages to Gemini format.
// Returns the system instruction (if any) and the conversation contents.
func toGeminiContents(messages []Message) (*geminiContent, []geminiContent) {
	var sysInstruction *geminiContent
	var contents []geminiContent

	for _, m := range messages {
		if m.Role == "system" {
			sysInstruction = &geminiContent{
				Role:  "user",
				Parts: []geminiPart{{Text: m.Content}},
			}
			continue
		}
		// Skip messages with no content and no files — Gemini rejects empty parts
		if m.Content == "" && len(m.Files) == 0 {
			continue
		}
		role := m.Role
		if role == "assistant" {
			role = "model"
		}
		var parts []geminiPart
		if m.Content != "" {
			parts = append(parts, geminiPart{Text: m.Content})
		}
		for _, f := range m.Files {
			parts = append(parts, geminiPart{
				InlineData: &geminiInline{
					MimeType: f.MimeType,
					Data:     base64.StdEncoding.EncodeToString(f.Data),
				},
			})
		}
		contents = append(contents, geminiContent{
			Role:  role,
			Parts: parts,
		})
	}
	return sysInstruction, contents
}

// Chat sends messages and returns the full response.
func (g *Gemini) Chat(ctx context.Context, messages []Message) (string, TokenCounts, error) {
	if err := checkProviderRPM(ctx, geminiBaseURL); err != nil {
		return "", TokenCounts{}, err
	}
	release, err := acquireSem(ctx, geminiBaseURL)
	if err != nil {
		return "", TokenCounts{}, err
	}
	defer release()
	sysInstruction, contents := toGeminiContents(messages)

	body := geminiRequest{
		Contents:          contents,
		SystemInstruction: sysInstruction,
	}
	data, err := json.Marshal(body)
	if err != nil {
		return "", TokenCounts{}, fmt.Errorf("marshal request: %w", err)
	}

	rawURL := fmt.Sprintf("%s/models/%s:generateContent?key=%s", geminiBaseURL, g.Model, url.QueryEscape(g.APIKey))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rawURL, bytes.NewReader(data))
	if err != nil {
		return "", TokenCounts{}, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := g.client.Do(req)
	if err != nil {
		return "", TokenCounts{}, fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	respData, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", TokenCounts{}, fmt.Errorf("read response: %w", err)
	}

	var result geminiResponse
	if err := json.Unmarshal(respData, &result); err != nil {
		return "", TokenCounts{}, fmt.Errorf("unmarshal response: %w", err)
	}

	if result.Error != nil {
		return "", TokenCounts{}, fmt.Errorf("gemini error [%d]: %s", result.Error.Code, result.Error.Message)
	}
	if len(result.Candidates) == 0 {
		return "", TokenCounts{}, fmt.Errorf("no candidates in response")
	}
	var tc TokenCounts
	if result.UsageMetadata != nil {
		tc = TokenCounts{Prompt: result.UsageMetadata.PromptTokenCount, Completion: result.UsageMetadata.CandidatesTokenCount}
	}

	var sb strings.Builder
	for _, part := range result.Candidates[0].Content.Parts {
		sb.WriteString(part.Text)
	}
	return sb.String(), tc, nil
}

// ChatStream sends messages and streams the response via onChunk callback.
func (g *Gemini) ChatStream(ctx context.Context, messages []Message, onChunk func(chunk string), onDone func(TokenCounts)) error {
	return withRateAndSem(ctx, geminiBaseURL, func() error {
	sysInstruction, contents := toGeminiContents(messages)

	body := geminiRequest{
		Contents:          contents,
		SystemInstruction: sysInstruction,
	}
	data, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	rawURL := fmt.Sprintf("%s/models/%s:streamGenerateContent?alt=sse&key=%s", geminiBaseURL, g.Model, url.QueryEscape(g.APIKey))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rawURL, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := g.client.Do(req)
	if err != nil {
		return fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respData, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("gemini error (status %d): %s", resp.StatusCode, string(respData))
	}

	var tc TokenCounts
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		payload := strings.TrimPrefix(line, "data: ")

		var chunk geminiResponse
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			continue
		}
		if len(chunk.Candidates) > 0 {
			for _, part := range chunk.Candidates[0].Content.Parts {
				if part.Text != "" {
					onChunk(part.Text)
				}
			}
		}
		if chunk.UsageMetadata != nil {
			tc = TokenCounts{Prompt: chunk.UsageMetadata.PromptTokenCount, Completion: chunk.UsageMetadata.CandidatesTokenCount}
		}
	}
	if onDone != nil {
		onDone(tc)
	}
	return scanner.Err()
	})
}

// ListModels returns available Gemini models.
func (g *Gemini) ListModels(ctx context.Context) ([]ModelInfo, error) {
	rawURL := fmt.Sprintf("%s/models?key=%s", geminiBaseURL, url.QueryEscape(g.APIKey))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	resp, err := g.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	var list geminiModelList
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}

	models := make([]ModelInfo, 0, len(list.Models))
	for _, m := range list.Models {
		// Strip "models/" prefix from name
		id := strings.TrimPrefix(m.Name, "models/")
		models = append(models, ModelInfo{
			ID:          id,
			Name:        m.DisplayName,
			Description: m.Description,
		})
	}
	return models, nil
}
