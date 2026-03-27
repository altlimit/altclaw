package bridge

import (
	"context"
	"strings"
	"time"

	"altclaw.ai/internal/config"
	"github.com/dop251/goja"
)

// RegisterChat adds the chat namespace to the runtime.
// Provides read-only access to other conversations in the same workspace.
//
//	chat.list(opts?)     → [{id, title, provider, created, modified}]
//	chat.read(id, opts?) → [{role, content, created}]
func RegisterChat(vm *goja.Runtime, store *config.Store, workspaceID string) {
	chatObj := vm.NewObject()

	// chat.list(opts?) — list workspace chats, newest first
	chatObj.Set("list", func(call goja.FunctionCall) goja.Value {
		limit := 20
		if len(call.Arguments) >= 1 {
			opts := call.Arguments[0].ToObject(vm)
			if v := opts.Get("limit"); v != nil && !goja.IsUndefined(v) {
				limit = int(v.ToInteger())
			}
		}

		chats, err := store.ListChats(context.Background(), workspaceID)
		if err != nil {
			logErr(vm, "chat.list", err)
		}

		if len(chats) > limit {
			chats = chats[:limit]
		}

		results := make([]interface{}, len(chats))
		for i, c := range chats {
			results[i] = map[string]interface{}{
				"id":       c.ID,
				"title":    c.Title,
				"provider": c.Provider,
				"created":  c.CreatedAt.Format(time.RFC3339),
				"modified": c.UpdatedAt.Format(time.RFC3339),
			}
		}
		return vm.ToValue(results)
	})

	// chat.read(chatID, opts?) — read messages from a specific chat
	chatObj.Set("read", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 1 {
			Throw(vm, "chat.read requires a chatID argument")
		}
		chatID := call.Arguments[0].ToInteger()

		limit := 50
		if len(call.Arguments) >= 2 {
			opts := call.Arguments[1].ToObject(vm)
			if v := opts.Get("limit"); v != nil && !goja.IsUndefined(v) {
				limit = int(v.ToInteger())
			}
		}

		chat, err := store.GetChat(context.Background(), workspaceID, chatID)
		if err != nil {
			logErr(vm, "chat.read", err)
		}

		msgs, err := store.ListChatMessages(context.Background(), chat)
		if err != nil {
			logErr(vm, "chat.read", err)
		}

		// Filter out exec-loop noise: skip "[Execution Results]" user messages
		// and assistant messages containing ```exec blocks
		var clean []*config.ChatMessage
		for _, m := range msgs {
			if m.Role == "user" && strings.HasPrefix(m.Content, "[Execution Results]") {
				continue
			}
			if m.Role == "assistant" && strings.Contains(m.Content, "```exec") {
				continue
			}
			clean = append(clean, m)
		}

		// Take last `limit` messages
		if len(clean) > limit {
			clean = clean[len(clean)-limit:]
		}

		results := make([]interface{}, len(clean))
		for i, m := range clean {
			results[i] = map[string]interface{}{
				"role":    m.Role,
				"content": m.Content,
				"created": m.CreatedAt.Format(time.RFC3339),
			}
		}
		return vm.ToValue(results)
	})

	vm.Set(NameChat, chatObj)
}
