// Package provider defines the AI model provider interface for Altclaw.
// Any LLM backend (OpenAI, Ollama, Anthropic, etc.) can be plugged in
// by implementing the Provider interface.
package provider

import (
	"context"
	"fmt"
	"sync"

	"altclaw.ai/internal/util"
)

// ── Concurrency limiter ──────────────────────────────────────────────────────

var (
	semMu   sync.Mutex
	semMap  = map[string]chan struct{}{} // key → buffered channel (semaphore)
	semSize int                          // 0 = unlimited
)

// SetConcurrency sets the maximum number of concurrent in-flight requests
// allowed per provider endpoint. Call this once at startup from appCfg.
// n <= 0 means unlimited (default).
func SetConcurrency(n int) {
	semMu.Lock()
	defer semMu.Unlock()
	semSize = n
	// Reset all existing semaphores to new size
	semMap = map[string]chan struct{}{}
}

// acquireSem blocks until a slot is available for the given endpoint key,
// then returns a release function. If concurrency is unlimited it is a no-op.
func acquireSem(ctx context.Context, key string) (release func(), err error) {
	semMu.Lock()
	n := semSize
	if n <= 0 {
		semMu.Unlock()
		return func() {}, nil
	}
	ch, ok := semMap[key]
	if !ok {
		ch = make(chan struct{}, n)
		semMap[key] = ch
	}
	semMu.Unlock()

	select {
	case ch <- struct{}{}:
		return func() { <-ch }, nil
	case <-ctx.Done():
		return func() {}, ctx.Err()
	}
}

// ── Token usage hook ─────────────────────────────────────────────────

// TokenCounts holds the token counts for a single provider call.
type TokenCounts struct {
	Prompt     int64
	Completion int64
}

// ── Per-provider RPM rate limit ───────────────────────────────────────

var (
	provRPMLimiter = util.NewSlidingWindowLimiter()
	provRPMMu      sync.Mutex
	provRPMMap     = map[string]int{} // key → configured RPM (0 = unlimited)
)

// SetProviderRPM configures a requests-per-minute limit for the given endpoint key.
// key is the same string used in acquireSem (baseURL / host).
// rpm <= 0 means unlimited.
func SetProviderRPM(key string, rpm int64) {
	provRPMMu.Lock()
	defer provRPMMu.Unlock()
	provRPMMap[key] = int(rpm)
}

// checkProviderRPM enforces the per-provider RPM limit using a sliding window.
// Returns an error if the limit is exceeded.
func checkProviderRPM(ctx context.Context, key string) error {
	provRPMMu.Lock()
	rpm := provRPMMap[key]
	provRPMMu.Unlock()
	return provRPMLimiter.Allow(key, int64(rpm))
}

// withRateAndSem combines rate-limit checking and semaphore acquisition into a
// single helper. Every provider Chat/ChatStream method should wrap its body with
// this instead of repeating the 6-line boilerplate.
func withRateAndSem(ctx context.Context, key string, fn func() error) error {
	if err := checkProviderRPM(ctx, key); err != nil {
		return err
	}
	release, err := acquireSem(ctx, key)
	if err != nil {
		return err
	}
	defer release()
	return fn()
}

// FileData represents a file attached to a message for multimodal AI analysis.
type FileData struct {
	Name     string // filename for display (e.g. "screenshot.png")
	MimeType string // MIME type (e.g. "image/png", "application/pdf")
	Data     []byte // raw file bytes
}

// Message represents a single chat message in the conversation.
type Message struct {
	Role    string     `json:"role"`    // "system", "user", "assistant"
	Content string     `json:"content"` // message text
	Files   []FileData `json:"-"`       // attached files (providers encode internally)
}

// ModelInfo describes an available model from a provider.
type ModelInfo struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// Provider is the interface that all AI model backends must implement.
type Provider interface {
	// Chat sends a conversation and returns the assistant's full response with token counts.
	Chat(ctx context.Context, messages []Message) (string, TokenCounts, error)

	// ChatStream sends a conversation and streams the response chunk-by-chunk.
	// onDone is called once with the final token counts when streaming completes.
	ChatStream(ctx context.Context, messages []Message, onChunk func(chunk string), onDone func(TokenCounts)) error

	// ListModels returns the available models from this provider.
	ListModels(ctx context.Context) ([]ModelInfo, error)

	// Name returns the human-readable name of this provider (e.g. "openai", "ollama").
	Name() string
}

// Build constructs a Provider from its type and credentials.
// This centralizes provider construction so both CLI and web API can use it.
// openaiCompatible defines default base URLs for OpenAI-compatible providers.
var openaiCompatible = map[string]string{
	"grok":         "https://api.x.ai/v1",
	"deepseek":     "https://api.deepseek.com/v1",
	"mistral":      "https://api.mistral.ai/v1",
	"openrouter":   "https://openrouter.ai/api/v1",
	"perplexity":   "https://api.perplexity.ai",
	"hugging_face": "https://api-inference.huggingface.co/v1",
	"minimax":      "https://api.minimaxi.chat/v1",
	"glm":          "https://open.bigmodel.cn/api/paas/v4",
}

func Build(providerType, apiKey, model, baseURL, host string) (Provider, error) {
	switch providerType {
	case "openai":
		if baseURL == "" {
			baseURL = "https://api.openai.com/v1"
		}
		if apiKey == "" {
			return nil, fmt.Errorf("OpenAI provider requires an API key")
		}
		return NewOpenAI(baseURL, apiKey, model), nil
	case "ollama":
		if host == "" {
			host = "http://localhost:11434"
		}
		return NewOllama(host, model), nil
	case "gemini":
		if apiKey == "" {
			return nil, fmt.Errorf("Gemini provider requires an API key")
		}
		return NewGemini(apiKey, model), nil
	case "anthropic", "claude":
		if apiKey == "" {
			return nil, fmt.Errorf("Anthropic provider requires an API key")
		}
		return NewAnthropic(apiKey, model), nil
	default:
		// Check OpenAI-compatible presets
		if defaultURL, ok := openaiCompatible[providerType]; ok {
			if baseURL == "" {
				baseURL = defaultURL
			}
			if apiKey == "" {
				return nil, fmt.Errorf("%s provider requires an API key", providerType)
			}
			return NewOpenAI(baseURL, apiKey, model), nil
		}
		return nil, fmt.Errorf("unknown provider %q", providerType)
	}
}
