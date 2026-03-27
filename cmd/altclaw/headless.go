package main

import (
	"context"
	"fmt"
	"log/slog"

	"altclaw.ai/internal/agent"
	"altclaw.ai/internal/config"
	"altclaw.ai/internal/cron"
)

// headlessUI implements bridge.UIHandler for non-interactive mode.
type headlessUI struct {
	LogFunc func(string) // optional override for Log output
}

func (h *headlessUI) Log(msg string) {
	if h.LogFunc != nil {
		h.LogFunc(msg)
	} else {
		fmt.Println(msg)
	}
}

func (h *headlessUI) Ask(question string) string {
	fmt.Printf("? %s: ", question)
	var answer string
	fmt.Scanln(&answer)
	return answer
}

func (h *headlessUI) Confirm(action, label, summary string, params map[string]any) string {
	fmt.Printf("🔐 Action requested: %s\n   %s\n   [auto-rejected in headless mode]\n", label, summary)
	return "no"
}

// cronSubAgentRunner wraps an agent as a SubAgentRunner for cron scripts.
// On first agent.run() call, if chatID is 0 (chat was deleted), it lazily
// creates a new chat and updates the cron job's chatID.
type cronSubAgentRunner struct {
	agent     *agent.Agent
	store     *config.Store
	wsID      string
	cronMgr   *cron.Manager
	jobID     int64
	chatID    *int64          // mutable — updated on lazy chat creation
	broadcast func(msg string) // optional broadcast to SSE
}

func (c *cronSubAgentRunner) ensureChat(ctx context.Context) {
	if *c.chatID > 0 {
		return
	}
	// Lazily create a new chat for this cron job
	chat, err := c.store.CreateChat(ctx, c.wsID, "⏰ Cron Task", "")
	if err != nil {
		slog.Warn("cron: failed to create chat", "error", err)
		return
	}
	*c.chatID = chat.ID
	// Update the cron job so future runs use this chat
	if c.cronMgr != nil {
		c.cronMgr.UpdateJobChatID(c.jobID, chat.ID)
	}
	slog.Info("cron: created chat for AI task", "chat_id", chat.ID, "job_id", c.jobID)
}

func (c *cronSubAgentRunner) RunSubAgent(ctx context.Context, task string) (string, error) {
	c.ensureChat(ctx)
	// Set ChatID so sub-agent history is linked to this chat
	origChatID := c.agent.ChatID
	c.agent.ChatID = *c.chatID
	result, err := c.agent.RunSubAgent(ctx, task)
	c.agent.ChatID = origChatID
	if err == nil && result != "" {
		c.chatMsg(ctx, task, result)
	}
	return result, err
}

func (c *cronSubAgentRunner) RunSubAgentWith(ctx context.Context, task, providerName string) (string, error) {
	c.ensureChat(ctx)
	origChatID := c.agent.ChatID
	c.agent.ChatID = *c.chatID
	result, err := c.agent.RunSubAgentWith(ctx, task, providerName)
	c.agent.ChatID = origChatID
	if err == nil && result != "" {
		c.chatMsg(ctx, task, result)
	}
	return result, err
}

// chatMsg persists the AI conversation to the chat and broadcasts the response.
func (c *cronSubAgentRunner) chatMsg(ctx context.Context, task, result string) {
	chatID := *c.chatID
	if chatID > 0 {
		// Persist messages so they show when the chat is opened/refreshed
		chat := &config.Chat{ID: chatID, Workspace: c.wsID}
		_ = c.store.AddChatMessage(ctx, chat, "user", "⏰ "+task, "")
		_ = c.store.AddChatMessage(ctx, chat, "assistant", result, "")
	}
	// Broadcast to SSE
	if c.broadcast != nil {
		c.broadcast("🤖 " + result)
	}
}
