// Package agent implements the core AI agent orchestrator loop.
// It sends user messages to an AI provider, extracts JS code blocks from responses,
// runs them in the Goja engine, and feeds results back into the conversation.
package agent

import (
	"context"
	"fmt"
	"hash/fnv"
	"log/slog"
	"regexp"
	"strings"
	"sync/atomic"
	"time"

	"altclaw.ai/internal/bridge"
	"altclaw.ai/internal/config"
	"altclaw.ai/internal/engine"
	"altclaw.ai/internal/executor"
	"altclaw.ai/internal/provider"
	"altclaw.ai/internal/util"
)

var codeBlockRe = regexp.MustCompile("(?si)`{3,}exec[^\n]*\r?\n(.*?)\n?`{3,}")
var subAgentCounter atomic.Int64

// callSigRe extracts known module-level API calls from code blocks.
var callSigRe = regexp.MustCompile(`\b(doc\.read|doc\.find|doc\.list|doc\.all|fs\.read|fs\.write|fs\.patch|fs\.append|fs\.list|fs\.grep|fetch|sys\.call|sys\.spawn|agent\.run|mem\.set|mem\.add|browser\.\w+|db\.\w+|ssh\.\w+|mail\.\w+|cache\.\w+|zip\.\w+|img\.\w+|cron\.\w+|task\.\w+|secret\.\w+|chat\.\w+|log\.\w+|git\.\w+|blob\.\w+|dns\.\w+)\s*\(([^)]{0,80})`)

// instanceCallRe captures instance method calls (e.g. client.read, conn.list, imap.close)
// using common API method names. This catches patterns the module-level regex misses.
var instanceCallRe = regexp.MustCompile(`\b(\w+)\.(connect|list|read|write|send|find|search|patch|append|grep|close|download|flag|move|set|get|add|remove|delete|create|update|query|exec|run|call|spawn|fetch)\s*\(([^)]{0,60})`)

// seenResult tracks a previously seen execution result for deduplication.
type seenResult struct {
	label   string // iteration label like "#1.1"
	preview string // truncated content from first occurrence
}

// fnvHash returns a 64-bit FNV-1a hash of s.
func fnvHash(s string) uint64 {
	h := fnv.New64a()
	h.Write([]byte(s))
	return h.Sum64()
}

// extractCallSignatures extracts API call names from code for the ledger.
// Uses two passes: first for known module calls, then for instance method calls.
// Returns a human-readable summary like "mail.connect({...}), client.list('INBOX', ...), client.read(uid)".
func extractCallSignatures(code string) string {
	seen := make(map[string]bool)
	var sigs []string

	// Pass 1: known module-level calls (highest priority)
	for _, m := range callSigRe.FindAllStringSubmatch(code, 5) {
		sig := m[1] + "(" + m[2] + ")"
		if !seen[sig] {
			seen[sig] = true
			sigs = append(sigs, sig)
		}
	}

	// Pass 2: instance method calls (e.g. client.read, conn.list)
	// Skip if we already have enough signatures
	if len(sigs) < 5 {
		for _, m := range instanceCallRe.FindAllStringSubmatch(code, 5) {
			// Skip JavaScript built-ins and noise
			obj := m[1]
			if obj == "console" || obj == "JSON" || obj == "Math" || obj == "Array" || obj == "Object" || obj == "String" || obj == "Date" || obj == "Promise" || obj == "m" || obj == "e" {
				continue
			}
			sig := obj + "." + m[2] + "(" + m[3] + ")"
			if !seen[sig] && len(sigs) < 7 {
				seen[sig] = true
				sigs = append(sigs, sig)
			}
		}
	}

	if len(sigs) == 0 {
		return "(script)"
	}
	return strings.Join(sigs, ", ")
}

// ledgerPreview truncates a result string for the iteration ledger.
// Small results (≤3000 chars) are never truncated — they're typically docs,
// configs, or short outputs that the AI needs in full as reference material.
// Large results use progressive truncation: earlier iterations get more room.
func ledgerPreview(result string, iteration int) string {
	const smallResultThreshold = 3000
	if len(result) <= smallResultThreshold {
		return result
	}
	// Progressive limit for large results
	limit := 800
	switch {
	case iteration >= 10:
		limit = 200
	case iteration >= 5:
		limit = 400
	}
	return result[:limit] + "\n…(truncated)"
}

// ── Rate limiter ─────────────────────────────────────────────────────
// Package-level sliding-window rate limiter keyed by workspace NS.

var agentRateLimiter = util.NewSlidingWindowLimiter()

// checkRateLimit returns an error if the workspace has exceeded `rpm` requests
// in the past 60 seconds; otherwise records the current timestamp and returns nil.
func checkRateLimit(wsNS string, rpm int64) error {
	return agentRateLimiter.Allow(wsNS, rpm)
}

const commonPrompt = `*** EXECUTION STRATEGY (THINK BEFORE CODING) ***
1. INTERNAL KNOWLEDGE FIRST: If you already know the answer (lyrics, facts, math, code explanations), respond directly with a ` + "```md" + ` block.
2. KEEP SCRIPTS DUMB: Only use ` + "```exec" + ` when you need real-time data, workspace files, or to perform system actions.
3. NO COMPLEX PARSING: Fetch the raw data (e.g., page text, file contents) and use your native AI reasoning to analyze it.
4. ALTCLAW SETTINGS VS PROJECT FILES: If the user asks to configure the environment (e.g., "public directory", tunneling, cron jobs) or asks about your capabilities, they mean Altclaw Workspace Settings. DO NOT scan the filesystem for project configuration files. Instead, ALWAYS execute output(doc.read(\"manual\")) to discover how to configure Altclaw or check capabilities.
5. DISCOVER BEFORE GUESSING: If you need to use an unknown custom module or check system constraints, ALWAYS invoke output(doc.find("keyword")) or output(doc.read("module")) first rather than guessing.
6. TOPIC CHANGES: If the user's new message is unrelated to the previous task, focus entirely on the new request — prior conversation is context only.

*** RESPONSE PROTOCOL ***
Do not include introductory or concluding filler.
Choose EXACTLY ONE block type per message:

Option A: Execute Code (to gather data or perform system actions)
` + "```exec" + `
// Write synchronous JS here using ONLY the Allowed APIs.
// Use ui.log("message") to show what you are doing, ui.ask("question") to prompt the user, or ui.confirm("action", params) to request approval for privileged operations.
// Call output(value) to evaluate this block. You will receive the value in the next conversation turn.
` + "```" + `

Option B: Present to User (to provide the final answer)
` + "```md" + `
Write your final Markdown response for the user here. This ends the conversation turn.
` + "```" + `

*** CRITICAL ENVIRONMENT RULES ***
1. CUSTOM RUNTIME: This is a custom JavaScript runtime.
2. SYNCHRONOUS ONLY: All JavaScript APIs are 100% synchronous.

*** LEARN FROM MISTAKES ***
When something fails or doesn't work as expected:
- Save the lesson: mem.add("what failed and what worked instead", "learned").
- After ONE failure, try a different approach or think if you can use your internal knowledge.
- READ THE DOCS: call doc.read("module") to see exact API signatures before using any module.
- NEVER retry the same code that just failed without meaningful changes.

[ Built-in ]
- doc: Module manuals and discovery (e.g., doc.find("search"), doc.list(), doc.all())
- fs: File system operations (read, write, patch, grep, list, etc.)
- mem: Long-term and short-term memory storage
- fetch: Global HTTP client - fetch(url: string, {headers?: object, method?: string, body?: string}) → {status, headers, text(), json()}
- Advanced: sys, cron, agent, task, secret, crypto, browser, db, blob, git, log, dns, cache, zip, img, ssh, mail, chat — doc.read("globals") for details

[ Globals & Workspace ]
- require("name"):          Load a module (e.g. require("web"))
- require("./relative.js"): Load a relative file from the current script's directory
- require("/abs/path.js"):  Load a workspace file by absolute workspace path
- store: In-memory object shared across iterations (use fs for persistence)
- process: Environment variables and context (e.g., process.env)
- sleep(ms): Synchronous execution pause
- output(value): Passes results back to AI for review. You will see it next turn.

*** GENERAL RULES ***
- Keep workspace clean: Use .agent/ for all generated files (.agent/tmp/ for temp data — tracked in agent git history, but /tmp/ may be deleted periodically).
- Link workspace files in responses: [name](ws:path/to/file) (clickable in chat).
- To create a reusable module, write it with fs.write("path/file.js", code) then load with require("./path/file.js").
- If utilizing Docker via sys.call(), note that the container may be minimal. Use require("pkg") to install required packages first if needed.
- SYSTEM CONSTRAINTS: To understand your capabilities, limits, or check what actions are allowed, use doc.find("constraints") or call doc.read("manual") first.`

const baseSystemPrompt = `You are a highly capable AI assistant and task manager helping the user. You have the ability to execute JavaScript to manage the user's system and perform tasks if needed.

*** TASK PLANNING (MANAGER ROLE) ***
- For large, complex, or multi-step requests, formulate a step-by-step to-do list to keep track of your progress.
- Store your to-do list in your state (e.g., store.todo = [...]) or a temporary file in .agent/tmp/ to maintain context across your execution turns.
- DELEGATION: Sub-agents run in isolated environments. They do not share your store memory. You must parse their final Markdown response (or read the files they generate) and update your own to-do list accordingly before moving to the next step.

` + commonPrompt

const subAgentPrompt = `You are a focused, specialized task executor.

` + commonPrompt + `

*** SUB-AGENT SPECIFIC RULES ***
- Your primary goal is to complete the delegated task efficiently and accurately.
- STATE MANAGEMENT: You have access to the store object to maintain state across your own execution iterations, but this state is LOCAL to you. It is NOT shared with the manager. 
- TASK REPORTING: You must explicitly state your success, failure, or the exact data requested in your final ` + "```md" + ` block so the manager can read it.
- If returning large amounts of data, write it to .agent/tmp/ and return the file path in your Markdown response instead.
- Keep your output clean and focused. Only include the exact data, code, or confirmation the manager needs to proceed, without conversational filler.`

// Agent orchestrates the conversation between user, AI provider, and JS engine.
type Agent struct {
	Provider         provider.Provider
	Engine           *engine.Engine
	Messages         []provider.Message
	Workspace        string
	Ws               *config.Workspace // workspace record (provides ID, PublicDir, TunnelHost, etc.)
	ChatID           int64             // chat session ID for message persistence
	TurnID           string            // unique ID for current conversation turn (set during Send)
	Timeout          time.Duration
	ExecutorInfo     string                                             // e.g. "docker:ubuntu:latest" or "local"
	Exec             executor.Executor                                  // executor for session cleanup
	OnChunk          func(chunk string)                                 // streaming callback
	OnLog            func(msg string)                                   // log callback
	NewEngine        func(sessionID string) *engine.Engine              // factory for creating independent engines
	NewProvider      func(providerName, model string) provider.Provider // factory for sub-agent providers
	ProviderImage    func(providerName string) string                   // resolves provider name → Docker image (empty = default)
	ProvidersSummary string                                             // system-prompt summary of specialist providers
	Store            *config.Store                                      // database store for memory and history
	ProviderCfg      *config.Provider                                   // provider config record (for per-provider caps)
	PendingMsgs      func() []string                                    // returns and clears any user messages sent mid-execution
	agentID          string                                             // unique ID for history file prefixes
	MaxIter          int                                                // max code execution iterations (configurable)
	isSubAgent       bool                                               // true if this is a sub-agent
	providerName     string                                             // named provider key (e.g. "coder"), empty for default
}

// New creates a new Agent with the given provider and engine.
func New(p provider.Provider, eng *engine.Engine, workspace string, timeout time.Duration) *Agent {
	id := fmt.Sprintf("%s", time.Now().Format("150405"))
	return &Agent{
		Provider:  p,
		Engine:    eng,
		Messages:  []provider.Message{},
		Workspace: workspace,
		Timeout:   timeout,
		agentID:   id,
		MaxIter:   20,
	}
}

// resolveProviderName returns the configured provider name (e.g. "coding")
// when set, falling back to the provider type name (e.g. "gemini").
func (a *Agent) resolveProviderName() string {
	if a.providerName != "" {
		return a.providerName
	}
	return a.Provider.Name()
}

// SetProviderName sets the configured provider name (e.g. "coding").
func (a *Agent) SetProviderName(name string) {
	a.providerName = name
}

// saveToHistory writes a code block to the store for debugging.
// Returns the history entry ID for display in logs.
func (a *Agent) saveToHistory(ctx context.Context, code, aiResponse string, iteration, blockNum int) int64 {
	if a.Store == nil {
		return 0
	}

	agentType := "main"
	if a.isSubAgent {
		agentType = "sub-agent"
	}

	h := &config.History{
		Chat:          a.Store.Client.Key(&config.Chat{ID: a.ChatID, Workspace: a.Ws.ID}),
		ChatMessageID: a.TurnID,
		Code:          code,
		Response:      aiResponse,
		AgentType:     agentType,
		Provider:      a.resolveProviderName(),
		Iteration:     iteration,
		Block:         blockNum,
	}
	if err := a.Store.AddHistory(ctx, h); err != nil {
		return 0
	}
	return h.ID
}

// updateHistoryResult updates a history entry with the execution result.
func (a *Agent) updateHistoryResult(ctx context.Context, histID int64, result string) {
	if a.Store == nil || histID == 0 {
		return
	}
	chat := &config.Chat{ID: a.ChatID, Workspace: a.Ws.ID}
	h, err := a.Store.GetHistory(ctx, chat, histID)
	if err != nil {
		return
	}
	h.Result = result
	_ = a.Store.AddHistory(ctx, h)
}

// persistMessage saves a message to the chat message store.
// tc is optional (variadic); if provided, token counts are attached to the assistant message.
func (a *Agent) persistMessage(ctx context.Context, role, content string, tc ...provider.TokenCounts) error {
	if a.Store == nil || a.ChatID == 0 || a.isSubAgent {
		return nil
	}
	id := ""
	if role == "assistant" {
		id = a.TurnID
	}
	var counts provider.TokenCounts
	if len(tc) > 0 {
		counts = tc[0]
	}
	return a.Store.AddChatMessage(ctx, &config.Chat{ID: a.ChatID, Workspace: a.Ws.ID}, role, content, id, counts.Prompt, counts.Completion)
}

// LoadMessages restores agent conversation state from persisted chat messages.
func (a *Agent) LoadMessages(ctx context.Context) error {
	if a.Store == nil || a.ChatID == 0 {
		return nil
	}
	msgs, err := a.Store.ListChatMessages(ctx, &config.Chat{ID: a.ChatID, Workspace: a.Ws.ID})
	if err != nil {
		return err
	}
	a.Messages = make([]provider.Message, 0, len(msgs))
	for _, m := range msgs {
		a.Messages = append(a.Messages, provider.Message{
			Role:    m.Role,
			Content: m.Content,
		})
	}
	return nil
}

// buildSystemPrompt constructs the full system prompt, including memory.md content if it exists.
func (a *Agent) buildSystemPrompt(ctx context.Context) string {
	var prompt string
	if a.isSubAgent {
		prompt = subAgentPrompt
	} else {
		prompt = baseSystemPrompt
	}

	prompt += "\n\n*** DYNAMIC ENVIRONMENT STATE ***\n"
	prompt += "- Current time: " + time.Now().Format("2006-01-02 15:04:05 MST") + "\n"

	// Add specialist provider info
	if a.ProvidersSummary != "" {
		prompt += "\n*** SPECIALIST PROVIDERS ***\n"
		prompt += "You can delegate tasks to these providers using the 'agent' module (run doc.read('agent') for syntax).\n"
		if a.providerName != "" {
			prompt += "WARNING: You are currently running as the \"" + a.providerName + "\" provider. Do NOT delegate to yourself.\n"
		}
		prompt += a.ProvidersSummary + "\n"
	}

	// Load memory from Store
	prompt += "\n*** MEMORY & RECALL ***\n"
	if a.Store != nil {
		prompt += a.Store.BuildMemoryPrompt(ctx, a.Workspace) + "\n"
	} else {
		prompt += "Status: Unavailable.\n"
	}

	return prompt
}

// Send processes a user message through the agent loop:
// 1. Add user message to conversation
// 2. Send to AI provider
// 3. If response contains JS code, execute it
// 4. Feed results back and repeat until final text response
func (a *Agent) Send(ctx context.Context, userMessage string) (string, error) {
	// Generate a unique turn ID for this conversation turn
	a.TurnID = fmt.Sprintf("%x", time.Now().UnixNano())

	a.Messages = append(a.Messages, provider.Message{
		Role:    "user",
		Content: userMessage,
	})
	a.persistMessage(ctx, "user", userMessage)

	// Build system prompt fresh each time (picks up memory.md changes)
	systemPrompt := a.buildSystemPrompt(ctx)

	// ── Rate limit & daily cap enforcement ──────────────────────────────
	if a.Ws != nil && a.Store != nil {
		// Per-minute rate limit (sliding window)
		wsNS := a.Ws.ID
		settings := a.Store.Settings()
		rpm := settings.RateLimit()
		// Per-provider RPM override
		if a.ProviderCfg != nil && a.ProviderCfg.RateLimit > 0 {
			rpm = a.ProviderCfg.RateLimit
		}
		if err := checkRateLimit(wsNS, rpm); err != nil {
			return "", err
		}

		promptCap := settings.DailyPromptCap()
		completionCap := settings.DailyCompletionCap()

		// Daily cap check (workspace-level)
		today, _ := a.Store.TodayTokenUsage(ctx, wsNS)
		if today != nil {
			if today.PromptTokens >= promptCap {
				return "", fmt.Errorf("daily input token cap of %d reached. Usage resets at midnight UTC", promptCap)
			}
			if today.CompletionTokens >= completionCap {
				return "", fmt.Errorf("daily output token cap of %d reached. Usage resets at midnight UTC", completionCap)
			}
		}

		// Per-provider cap check (provider-level override)
		if a.ProviderCfg != nil && (a.ProviderCfg.DailyPromptCap > 0 || a.ProviderCfg.DailyCompletionCap > 0) {
			provToday, _ := a.Store.TodayTokenUsage(ctx, wsNS, a.ProviderCfg.ID)
			if provToday != nil {
				if a.ProviderCfg.DailyPromptCap > 0 && provToday.PromptTokens >= a.ProviderCfg.DailyPromptCap {
					return "", fmt.Errorf("provider '%s' daily input cap of %d reached. Usage resets at midnight UTC", a.providerName, a.ProviderCfg.DailyPromptCap)
				}
				if a.ProviderCfg.DailyCompletionCap > 0 && provToday.CompletionTokens >= a.ProviderCfg.DailyCompletionCap {
					return "", fmt.Errorf("provider '%s' daily output cap of %d reached. Usage resets at midnight UTC", a.providerName, a.ProviderCfg.DailyCompletionCap)
				}
			}
		}
	}

	// Smart history window: keep prior conversation lean, current turn complete.
	// For prior turns: strip intermediate exec messages (code blocks + "[Execution Results]"
	// feedback), keeping only actual user questions and final assistant responses.
	// Current turn messages (exec loop) are all kept for iterative context.
	maxPriorMessages := 10
	if a.Ws != nil && a.Store != nil {
		maxPriorMessages = int(a.Store.Settings().MessageWindow())
	}
	turnStartIdx := len(a.Messages) - 1 // index of the user message we just appended

	// Filter prior messages: skip exec loop noise from earlier turns
	var priorClean []provider.Message
	for _, m := range a.Messages[:turnStartIdx] {
		if m.Role == "user" && strings.HasPrefix(m.Content, "[Execution Results]") {
			continue // skip exec result feedback
		}
		if m.Role == "assistant" && strings.Contains(m.Content, "```exec") {
			continue // skip intermediate code responses
		}
		priorClean = append(priorClean, m)
	}
	if len(priorClean) > maxPriorMessages {
		priorClean = priorClean[len(priorClean)-maxPriorMessages:]
	}

	msgs := make([]provider.Message, 0, len(priorClean)+4)
	msgs = append(msgs, provider.Message{
		Role:    "system",
		Content: systemPrompt,
	})
	msgs = append(msgs, priorClean...)

	// Inject last successful execution context from DB history.
	// This gives the AI "here's how I did it last time" so it doesn't
	// re-learn API patterns from scratch each turn.
	if a.Store != nil && a.ChatID != 0 && !a.isSubAgent {
		chat := &config.Chat{ID: a.ChatID, Workspace: a.Ws.ID}
		if history, err := a.Store.ListHistoryByChat(ctx, chat); err == nil && len(history) > 0 {
			// Take last few successful (non-error) entries from PREVIOUS turns
			var contextLines []string
			seen := make(map[string]bool)
			for _, h := range history {
				// Skip entries from the current turn
				if h.ChatMessageID == a.TurnID {
					continue
				}
				// Skip errors
				if strings.HasPrefix(h.Result, "Error:") {
					continue
				}
				// Deduplicate by code content
				if seen[h.Code] {
					continue
				}
				seen[h.Code] = true

				// Compact: show code + truncated result
				result := h.Result
				if len(result) > 200 {
					result = result[:200] + "…"
				}
				contextLines = append(contextLines, fmt.Sprintf("```js\n%s\n```\nResult: %s", h.Code, result))
				if len(contextLines) >= 3 {
					break
				}
			}
			if len(contextLines) > 0 {
				contextMsg := "[Recent successful executions in this chat — reuse these patterns]\n" +
					strings.Join(contextLines, "\n\n")
				msgs = append(msgs, provider.Message{
					Role:    "user",
					Content: contextMsg,
				}, provider.Message{
					Role:    "assistant",
					Content: "Noted — I'll reuse these working patterns.",
				})
			}
		}
	}

	msgs = append(msgs, a.Messages[turnStartIdx]) // current user message

	var totalTurnTokens provider.TokenCounts
	var consecutiveErrors int
	var consecutiveSmallResults int // tracks iterations where all results were short/empty (no errors)

	// Iteration ledger: content-addressed dedup of execution results.
	// Tracks what was done each iteration so the AI never loses context,
	// even when older messages are compacted away.
	resultCache := make(map[uint64]seenResult)
	var ledgerEntries []string

	for i := 0; i < a.MaxIter; i++ {
		// Check for cancellation at the start of each iteration
		if ctx.Err() != nil {
			return "", fmt.Errorf("stopped")
		}

		// Drain any user messages sent mid-execution
		if a.PendingMsgs != nil {
			if pending := a.PendingMsgs(); len(pending) > 0 {
				for _, pm := range pending {
					userInjected := provider.Message{
						Role:    "user",
						Content: "[User Message (sent during execution)]: " + pm + "\nThe user sent this while you were working. Address it now if urgent, or note it as a to-do for later.",
					}
					a.Messages = append(a.Messages, userInjected)
					msgs = append(msgs, userInjected)
				}
			}
		}

		var response string
		var err error
		var turnTokens provider.TokenCounts

		// Log progress on subsequent iterations so user knows we're waiting
		if i > 0 && a.OnLog != nil {
			a.OnLog("🤖 Waiting for AI response...")
		}

		// Add timeout for the API call itself (separate from code execution timeout)
		apiCtx, apiCancel := context.WithTimeout(ctx, 120*time.Second)

		// For InMemory (profile) providers, inject relay routing into the context.
		// The RelayTransport will intercept http.Client calls and POST them to the relay's
		// /forward endpoint, passing the encrypted key opaquely for relay-side decryption.
		if a.ProviderCfg != nil && a.ProviderCfg.InMemory && a.Ws != nil {
			relayForwardURL := a.ProviderCfg.BaseURL // set to relay/forward by applyProfile
			// TunnelHost is the full URL (e.g. 'abc123-relay.altclaw.ai').
			// The relay keys tunnels by the bare hostname before '-relay.', so strip that suffix.
			relayKey, _, _ := strings.Cut(a.Ws.TunnelHost, "-relay.")
			apiCtx = provider.WithRelay(apiCtx, provider.RelayConfig{
				ForwardURL:  relayForwardURL,
				TunnelHost:  relayKey,
				TunnelToken: a.Ws.TunnelToken,
				AuthEnc:     a.ProviderCfg.APIKey, // api_key_enc blob; relay decrypts with HUB_SECRET
			})
		}

		slog.Debug("provider request", "provider", a.resolveProviderName(), "iteration", i, "messages", len(msgs))
		apiStart := time.Now()

		const maxRetries = 3
		for attempt := 1; attempt <= maxRetries; attempt++ {
			if a.OnChunk != nil {
				var sb strings.Builder
				// md fence filter: only forward content inside ```md blocks
				var (
					lineBuf   string
					inMdBlock bool
				)
				var streamTC provider.TokenCounts
				err = a.Provider.ChatStream(apiCtx, msgs, func(chunk string) {
					sb.WriteString(chunk)
					// Buffer and detect md fence boundaries
					lineBuf += chunk
					for {
						nlIdx := strings.Index(lineBuf, "\n")
						if nlIdx < 0 {
							// No complete line; if inside md block, forward partial chunk
							if inMdBlock {
								a.OnChunk(chunk)
							}
							break
						}
						line := lineBuf[:nlIdx+1]
						lineBuf = lineBuf[nlIdx+1:]
						trimmed := strings.TrimSpace(line)

						if !inMdBlock {
							// Opening: ```md or ````md etc.
							if strings.HasPrefix(trimmed, "```") && strings.HasSuffix(trimmed, "md") {
								inMdBlock = true
								continue
							}
						} else {
							// Closing: ``` (just backticks, no language tag)
							if strings.HasPrefix(trimmed, "```") && strings.TrimLeft(trimmed, "`") == "" {
								inMdBlock = false
								continue
							}
							a.OnChunk(line)
						}
					}
				}, func(tc provider.TokenCounts) {
					streamTC = tc
				})
				if err == nil {
					turnTokens = streamTC
				}
				response = sb.String()
			} else {
				var tc provider.TokenCounts
				response, tc, err = a.Provider.Chat(apiCtx, msgs)
				if err == nil {
					turnTokens = tc
				}
			}

			if err == nil {
				break
			}
			if ctx.Err() != nil {
				apiCancel()
				return "", fmt.Errorf("stopped")
			}
			if attempt < maxRetries {
				backoff := time.Duration(attempt) * 2 * time.Second
				slog.Warn("provider error, retrying", "attempt", attempt, "backoff", backoff, "error", err)
				if a.OnLog != nil {
					a.OnLog(fmt.Sprintf("⚠️ Provider error, retrying in %s... (attempt %d/%d)", backoff, attempt, maxRetries))
				}
				time.Sleep(backoff)
				// Reset stream state for retry
				if a.OnChunk != nil {
					a.OnChunk("\n") // signal client to discard partial
				}
			}
		}
		apiCancel()

		slog.Debug("provider response", "provider", a.resolveProviderName(), "iteration", i, "elapsed", time.Since(apiStart).Round(time.Millisecond))

		if err != nil {
			if ctx.Err() != nil {
				return "", fmt.Errorf("stopped")
			}
			return "", fmt.Errorf("ai provider error: %w", sanitizeProviderError(err))
		}

		a.Messages = append(a.Messages, provider.Message{
			Role:    "assistant",
			Content: response,
		})

		// Extract tagged blocks from AI response
		mdContent, codeBlocks := extractBlocks(response)

		// Persist token counts: increment workspace total + per-provider row (always, for filter/reporting)
		if a.Store != nil && a.Ws != nil && (turnTokens.Prompt > 0 || turnTokens.Completion > 0) {
			wsNS := a.Ws.ID
			if a.ProviderCfg != nil && a.ProviderCfg.ID > 0 {
				_ = a.Store.IncrementTokenUsage(ctx, wsNS, turnTokens.Prompt, turnTokens.Completion, a.ProviderCfg.ID)
			} else {
				_ = a.Store.IncrementTokenUsage(ctx, wsNS, turnTokens.Prompt, turnTokens.Completion)
			}
		}

		if len(codeBlocks) == 0 {
			// No exec blocks — md block is the response (full response = backward compat)
			display := mdContent
			if display == "" {
				display = response
			}
			a.persistMessage(ctx, "assistant", display, turnTokens)
			a.compactMessages()
			a.autoCommitGit()
			return display, nil
		}

		totalTurnTokens.Prompt += turnTokens.Prompt
		totalTurnTokens.Completion += turnTokens.Completion

		// Execute each code block
		var execResults []string
		for j, code := range codeBlocks {
			// Save to history for debugging (includes AI response)
			histID := a.saveToHistory(ctx, code, response, i, j)

			if a.OnLog != nil {
				a.OnLog(fmt.Sprintf("⚡ Running [%d/%d]: #%d", j+1, len(codeBlocks), histID))
			}

			// Capture ui.log output during execution
			var logCapture []string
			origOnLog := a.OnLog
			a.OnLog = func(msg string) {
				logCapture = append(logCapture, msg)
				if origOnLog != nil {
					origOnLog(msg)
				}
			}

			// Timeout per code block — pauses automatically when agent.result() blocks
			execCtx, cancel := context.WithTimeout(ctx, a.Timeout)
			result := a.Engine.Run(execCtx, code)
			cancel()

			// Restore original OnLog
			a.OnLog = origOnLog

			var resultStr string
			if result.Error != nil {
				consecutiveErrors++
				resultStr = fmt.Sprintf("Error: %v", result.Error)
				execResults = append(execResults, resultStr)
				if a.OnLog != nil {
					a.OnLog("❌ " + resultStr)
				}
			} else if result.Value != "" {
				consecutiveErrors = 0
				resultStr = fmt.Sprintf("Result: %s", result.Value)
				execResults = append(execResults, resultStr)
			} else if len(logCapture) > 0 {
				consecutiveErrors = 0
				// Log messages were already shown to user in real-time;
				// only send a count summary to save tokens.
				resultStr = fmt.Sprintf("Executed successfully (%d log messages shown to user)", len(logCapture))
				execResults = append(execResults, resultStr)
			} else {
				consecutiveErrors = 0
				resultStr = "Executed successfully (no output)"
				execResults = append(execResults, resultStr)
			}

			// Save full result to history (untruncated for debugging)
			a.updateHistoryResult(ctx, histID, resultStr)

			// Truncate large results before feeding back to the AI to save tokens
			const maxResultLen = 4000
			if len(resultStr) > maxResultLen {
				execResults[len(execResults)-1] = resultStr[:maxResultLen] + "\n... (truncated)"
			}

			// ── Ledger entry ───────────────────────────────────────────
			// Record what was called and what came back, deduplicating
			// identical results via FNV hash (like a zip dictionary).
			action := extractCallSignatures(code)
			hash := fnvHash(resultStr)
			label := fmt.Sprintf("#%d.%d", i+1, j+1)

			if seen, dup := resultCache[hash]; dup {
				// Duplicate — reference the original
				ledgerEntries = append(ledgerEntries,
					fmt.Sprintf("%s: %s → [same as %s, %d chars — already seen]",
						label, action, seen.label, len(resultStr)))
			} else {
				// First occurrence — store preview
				preview := ledgerPreview(resultStr, i)
				resultCache[hash] = seenResult{label: label, preview: preview}
				ledgerEntries = append(ledgerEntries,
					fmt.Sprintf("%s: %s → %d chars\n  %s",
						label, action, len(resultStr), preview))
			}
		}

		// done() no longer terminates — results are always fed back to the AI.
		// The AI reviews its own output and decides: md (present) or exec (continue).

		// exec and md are mutually exclusive: when exec blocks were present,
		// always feed results back so the AI sees actual output before presenting.
		if mdContent != "" {
			slog.Warn("ignoring md block in response that also contained exec blocks")
		}

		// Feed execution results back to the conversation
		execSummary := strings.Join(execResults, "\n")

		// Track consecutive iterations with small/empty results (non-error).
		// If every result in this iteration was short, the data is genuinely
		// sparse and the AI should present it rather than retry.
		if consecutiveErrors == 0 && len(execResults) > 0 {
			allSmall := true
			for _, r := range execResults {
				if len(r) > 50 {
					allSmall = false
					break
				}
			}
			if allSmall {
				consecutiveSmallResults++
			} else {
				consecutiveSmallResults = 0
			}
		} else {
			consecutiveSmallResults = 0
		}

		// Drain any files attached via ui.file() during execution
		pendingFiles := a.Engine.DrainFiles()

		var hint string
		if i >= a.MaxIter-1 {
			hint = "This is your final iteration. Respond with a ```md block now — no more code."
		} else if strings.Contains(execSummary, "user rejected:") {
			hint = "The user explicitly rejected this action. Do NOT retry or re-prompt for the same action. Respond with a ```md block acknowledging the rejection."
		} else if consecutiveErrors >= 3 {
			hint = "You have failed multiple times in a row. Use ui.ask() to ask the user for clarification or guidance, or respond with a ```md block explaining what went wrong."
		} else if consecutiveErrors >= 2 {
			hint = "You have failed twice consecutively. Try a COMPLETELY different approach, use ui.ask() to ask the user for help, or respond with a ```md block."
		} else if i >= a.MaxIter-2 {
			hint = "You have 1 iteration left after this. Wrap up your work and prepare a ```md response."
		} else if consecutiveSmallResults >= 2 {
			hint = "Your last " + fmt.Sprintf("%d", consecutiveSmallResults) + " attempts returned empty or minimal results. " +
				"Empty results ARE valid data — the resource may genuinely be empty. " +
				"Present your findings to the user with a ```md block instead of retrying."
		} else {
			hint = "Review the results. Respond with ```md to present the answer, or ```exec to continue working."
		}

		// Build accumulated ledger for this feedback message.
		// The ledger is the single source of truth for what happened across
		// all iterations — even after aggressive compaction of older messages.
		var ledgerBlock string
		if len(ledgerEntries) > 1 {
			// Only include ledger when there's history (>1 because current iteration
			// was just appended above; on first iteration there's only 1 entry).
			ledgerBlock = "\n[Iteration Log — do NOT re-read/repeat data you already have]\n" +
				strings.Join(ledgerEntries, "\n") + "\n"
		}

		feedback := provider.Message{
			Role:    "user",
			Content: fmt.Sprintf("[Execution Results] (iteration %d/%d)\n%s%s\n\n%s", i+1, a.MaxIter, execSummary, ledgerBlock, hint),
			Files:   pendingFiles,
		}
		a.Messages = append(a.Messages, feedback)

		// Compact code blocks in the current response for token savings.
		// Embed call signatures so the AI retains the working patterns.
		compactCode := func(content string) string {
			return codeBlockRe.ReplaceAllStringFunc(content, func(block string) string {
				inner := codeBlockRe.FindStringSubmatch(block)
				if len(inner) > 1 {
					sigs := extractCallSignatures(inner[1])
					if sigs != "(script)" {
						return "```exec\n// Calls: " + sigs + "\n[code omitted]\n```"
					}
				}
				return "```exec\n[code omitted]\n```"
			})
		}

		// Compact older iterations, but preserve the LAST iteration's code
		// and results intact so the AI has full context of what it just tried.
		// This prevents the AI from retrying the same operation because it
		// lost context about its most recent execution.
		var lastExecIdx int = -1
		var lastAssistantIdx int = -1
		for idx := range msgs {
			if msgs[idx].Role == "user" && strings.HasPrefix(msgs[idx].Content, "[Execution Results]") {
				if lastExecIdx >= 0 {
					msgs[lastExecIdx].Content = "[Execution Results] (see iteration log in latest results)"
				}
				lastExecIdx = idx
			}
			if msgs[idx].Role == "assistant" && strings.Contains(msgs[idx].Content, "```exec") {
				if lastAssistantIdx >= 0 {
					msgs[lastAssistantIdx].Content = compactCode(msgs[lastAssistantIdx].Content)
				}
				lastAssistantIdx = idx
			}
		}
		// lastExecIdx / lastAssistantIdx point to the most recent — kept intact.

		// Current iteration: add FULL response (uncompacted) so it becomes the
		// "last" intact pair. On the next iteration the loop above will compact it.
		msgs = append(msgs, provider.Message{Role: "assistant", Content: response}, feedback)
	}

	a.compactMessages()
	a.autoCommitGit()
	if a.isSubAgent {
		return "", fmt.Errorf("maximum execution iterations reached for sub-agent")
	}
	maxIterMsg := "Maximum execution iterations reached. Please try a simpler request."
	a.persistMessage(ctx, "assistant", maxIterMsg, totalTurnTokens)
	return maxIterMsg, nil
}

// autoCommitGit snapshots the workspace into the agent's git history repo.
// Runs silently — errors are logged but never surfaced to the user.
func (a *Agent) autoCommitGit() {
	if a.Ws == nil || a.isSubAgent {
		return
	}
	go bridge.AutoCommit(a.Workspace, config.ConfigDir(), a.Ws.ID, a.TurnID, a.resolveProviderName())
}

// compactMessages removes intermediate exec-loop noise from a.Messages to
// prevent unbounded memory growth in long-running sessions. Strips the same
// categories that the sliding window already filters for prior turns:
// synthetic "[Execution Results]" feedback and assistant code responses.
func (a *Agent) compactMessages() {
	clean := make([]provider.Message, 0, len(a.Messages)/2)
	for _, m := range a.Messages {
		if m.Role == "user" && strings.HasPrefix(m.Content, "[Execution Results]") {
			continue
		}
		if m.Role == "assistant" && strings.Contains(m.Content, "```exec") {
			continue
		}
		clean = append(clean, m)
	}
	a.Messages = clean
}

// Reset clears the conversation messages only.
func (a *Agent) Reset() {
	a.Messages = nil
}

// RunSubAgent implements bridge.SubAgentRunner.
// It creates a fresh sub-agent with its own Goja VM to avoid deadlocking the main engine.
func (a *Agent) RunSubAgent(ctx context.Context, task string) (string, error) {
	return a.runSubAgentInternal(ctx, task, a.Provider, a.providerName)
}

// RunSubAgentWith implements bridge.SubAgentRunner.
// Like RunSubAgent but uses a named provider for the sub-agent.
// If the requested provider matches the current agent's provider, it avoids
// self-delegation by using the same provider directly.
func (a *Agent) RunSubAgentWith(ctx context.Context, task, providerName string) (string, error) {
	prov := a.Provider // default fallback
	resolveName := providerName
	if a.NewProvider != nil && providerName != "" {
		// Detect self-delegation: error out instead of allowing infinite loops
		if providerName == a.providerName {
			return "", fmt.Errorf("cannot delegate to yourself (%q). Use agent.run(task) with a different provider", providerName)
		}
		prov = a.NewProvider(providerName, "")
		resolveName = providerName
	}
	return a.runSubAgentInternal(ctx, task, prov, resolveName)
}

// runSubAgentInternal is the shared implementation for sub-agent execution.
func (a *Agent) runSubAgentInternal(ctx context.Context, task string, prov provider.Provider, provName string) (string, error) {
	n := subAgentCounter.Add(1)
	subID := fmt.Sprintf("sub%d_%s", n, time.Now().Format("150405"))

	// Create a fresh engine for this sub-agent (own Goja VM)
	var subEngine *engine.Engine
	if a.NewEngine != nil {
		subEngine = a.NewEngine(subID)
	} else {
		// Fallback: reuse main engine (will serialize via mutex, won't deadlock
		// if not called from within JS execution)
		subEngine = a.Engine
	}

	sub := &Agent{
		Provider:         prov,
		Engine:           subEngine,
		Messages:         []provider.Message{},
		Workspace:        a.Workspace,
		Ws:               a.Ws,
		ChatID:           a.ChatID,
		Timeout:          a.Timeout,
		OnLog:            a.OnLog,
		Exec:             a.Exec,
		ExecutorInfo:     a.ExecutorInfo,
		ProvidersSummary: a.ProvidersSummary,
		Store:            a.Store,
		NewEngine:        a.NewEngine,
		NewProvider:      a.NewProvider,
		ProviderImage:    a.ProviderImage,
		agentID:          subID,
		MaxIter:          10,
		isSubAgent:       true,
		providerName:     provName,
	}

	// If this provider has a custom Docker image, set it for this session
	if a.Exec != nil && a.ProviderImage != nil && provName != "" {
		if img := a.ProviderImage(provName); img != "" {
			a.Exec.SetImage(img, executor.ImageOpts{}, subID)
			sub.ExecutorInfo = "docker:" + img
		}
	}

	// Give sub-agents the ability to delegate to specialist providers
	if a.NewEngine != nil {
		subEngine.SetAgentRunner(sub)
		defer subEngine.Cleanup()
	}
	// Cleanup session-specific Docker container when sub-agent finishes
	if a.Exec != nil {
		defer a.Exec.CleanupSession(subID)
	}

	return sub.Send(ctx, task)
}

// extractBlocks extracts ```md content and ```exec code blocks from an AI response.
// Since we enforce exec or md format, md extraction is simple fence stripping.
func extractBlocks(text string) (string, []string) {
	// Extract exec blocks (regex needed since there can be multiple)
	execMatches := codeBlockRe.FindAllStringSubmatch(text, -1)
	codeBlocks := make([]string, 0, len(execMatches))
	for _, m := range execMatches {
		code := strings.TrimSpace(m[1])
		if code != "" {
			codeBlocks = append(codeBlocks, code)
		}
	}

	// Extract md content by stripping fences
	var mdContent string
	trimmed := strings.TrimSpace(text)
	if strings.HasPrefix(trimmed, "```md") {
		// Strip opening fence line
		if idx := strings.Index(trimmed, "\n"); idx >= 0 {
			content := trimmed[idx+1:]
			// Strip closing fence
			if end := strings.LastIndex(content, "```"); end >= 0 {
				content = content[:end]
			}
			mdContent = strings.TrimSpace(content)
		}
	}

	return mdContent, codeBlocks
}

// apiKeyRe matches URL query params that contain API keys.
var apiKeyRe = regexp.MustCompile(`([?&])(key|api_key|apikey|token|access_token)=[^&"\s]+`)

// sanitizeProviderError strips API keys and sensitive info from error messages.
func sanitizeProviderError(err error) error {
	msg := err.Error()
	msg = apiKeyRe.ReplaceAllString(msg, "${1}${2}=REDACTED")
	// Also redact Bearer tokens
	if idx := strings.Index(msg, "Bearer "); idx >= 0 {
		end := strings.IndexAny(msg[idx+7:], "\" \n")
		if end < 0 {
			msg = msg[:idx+7] + "REDACTED"
		} else {
			msg = msg[:idx+7] + "REDACTED" + msg[idx+7+end:]
		}
	}
	return fmt.Errorf("%s", msg)
}
