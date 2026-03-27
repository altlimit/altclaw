package web

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"

	"altclaw.ai/internal/config"
	"github.com/altlimit/restruct"
)

// getOrCreateSession gets an existing chat session or creates a new one.
func (a *Api) getOrCreateSession(ctx context.Context, chatID int64, providerName string) (*chatSession, error) {
	a.server.mu.Lock()
	if sess, ok := a.server.chats[chatID]; ok {
		a.server.mu.Unlock()
		return sess, nil
	}
	a.server.mu.Unlock()

	// Build a fresh agent for this chat
	if a.server.NewAgent == nil {
		return nil, fmt.Errorf("agent not configured")
	}
	ag, err := a.server.NewAgent(providerName)
	if err != nil {
		return nil, err
	}
	ag.ChatID = chatID
	// Restore messages from DB
	_ = ag.LoadMessages(ctx)

	sess := &chatSession{
		agent:   ag,
		askChan: make(chan string),
	}
	a.server.mu.Lock()
	a.server.chats[chatID] = sess
	a.server.mu.Unlock()
	return sess, nil
}

// Chat handles chat messages. Starts agent in background and returns chat_id.
// Events are broadcast via the EventHub SSE stream.
func (a *Api) Chat(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Message  string `json:"message"`
		ChatID   int64  `json:"chat_id"`
		Provider string `json:"provider"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}
	if req.Message == "" {
		http.Error(w, "Message required", http.StatusBadRequest)
		return
	}

	wsID := a.server.store.Workspace().ID

	// Create new chat if chat_id is 0
	if req.ChatID == 0 {
		title := req.Message
		if len(title) > 50 {
			title = title[:50] + "..."
		}
		chat, err := a.server.store.CreateChat(r.Context(), wsID, title, req.Provider)
		if err != nil {
			http.Error(w, "Failed to create chat: "+err.Error(), http.StatusInternalServerError)
			return
		}
		req.ChatID = chat.ID
	}

	sess, err := a.getOrCreateSession(r.Context(), req.ChatID, req.Provider)
	if err != nil {
		slog.Error("failed to create agent session", "chat_id", req.ChatID, "error", err)
		http.Error(w, "Service unavailable, please try again.", http.StatusServiceUnavailable)
		return
	}

	// Check if already running
	sess.mu.Lock()
	if sess.running {
		sess.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"chat_id": req.ChatID, "status": "already_running"})
		return
	}

	sess.running = true
	sess.pendingEvents = nil   // clear any stale buffer
	sess.pendingUserMsgs = nil // clear any stale pending messages
	hub := a.server.hub
	chatID := req.ChatID

	ag := sess.agent
	agCtx, agCancel := context.WithCancel(context.Background())
	sess.cancel = agCancel
	sess.ctx = agCtx
	sess.mu.Unlock()

	// Broadcast meta event
	sess.bufferAndBroadcast(hub, chatEvent{Type: "meta", ChatID: chatID})

	ws := a.server.store.Workspace()
	// Only stream AI chunks if ShowThinking is enabled
	if a.server.store.Settings().ShowThinking() {
		ag.OnChunk = func(chunk string) {
			sess.bufferAndBroadcast(hub, chatEvent{Type: "chunk", Content: chunk, ChatID: chatID})
		}
	} else {
		ag.OnChunk = nil
	}
	ag.OnLog = func(msg string) {
		sess.bufferAndBroadcast(hub, chatEvent{Type: "log", Content: msg, ChatID: chatID})
	}
	ag.PendingMsgs = func() []string {
		sess.mu.Lock()
		msgs := sess.pendingUserMsgs
		sess.pendingUserMsgs = nil
		sess.mu.Unlock()
		return msgs
	}

	// Start agent in background
	go func() {
		result, err := ag.Send(agCtx, req.Message)
		if err != nil {
			slog.Error("agent error", "chat_id", chatID, "error", err)
			msg := "Something went wrong, please try again."
			sess.bufferAndBroadcast(hub, chatEvent{Type: "error", Content: msg, ChatID: chatID})
			a.server.maybePush("Altclaw Error", msg)
		} else {
			sess.bufferAndBroadcast(hub, chatEvent{Type: "done", Content: result, ChatID: chatID, MessageID: ag.TurnID})
			// Truncate push body to a readable length
			body := result
			if len(body) > 120 {
				body = body[:120] + "…"
			}
			a.server.maybePush("Altclaw", body)
		}

		// Update chat's modified timestamp
		if chat, err := a.server.store.GetChat(context.Background(), ws.ID, chatID); err == nil {
			_ = a.server.store.UpdateChat(context.Background(), chat)
		}

		sess.mu.Lock()
		sess.running = false
		sess.cancel = nil
		sess.pendingEvents = nil   // clear buffer after execution completes
		sess.pendingUserMsgs = nil // clear any undelivered pending messages
		sess.mu.Unlock()
	}()

	// Return immediately with chat_id
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"chat_id": chatID})
}

// Stop cancels a specific chat execution.
func (a *Api) Stop(req struct {
	ChatID int64 `json:"chat_id"`
}) map[string]string {
	a.server.mu.Lock()
	sess, ok := a.server.chats[req.ChatID]
	a.server.mu.Unlock()
	if ok && sess.cancel != nil {
		sess.cancel()
	}
	return map[string]string{"status": "stopped"}
}

// Chats_0_Clear resets a specific chat's conversation.
func (a *Api) Chats_0_Clear(ctx context.Context) any {
	chatID, _ := strconv.ParseInt(restruct.Vars(ctx)["0"], 10, 64)
	if chatID == 0 {
		return restruct.Error{Status: http.StatusBadRequest, Message: "chat_id required"}
	}
	// Clear in-memory session
	a.server.mu.Lock()
	delete(a.server.chats, chatID)
	a.server.mu.Unlock()
	// Clear persisted messages
	wsID := a.server.store.Workspace().ID
	if err := a.server.store.ClearChatMessages(ctx, &config.Chat{ID: chatID, Workspace: wsID}); err != nil {
		return restruct.Error{Status: http.StatusInternalServerError, Err: err}
	}
	return map[string]string{"status": "cleared"}
}

// Chats lists chat sessions with optional pagination.
func (a *Api) Chats(r *http.Request) any {
	limitStr := r.URL.Query().Get("limit")
	cursor := r.URL.Query().Get("cursor")

	ws := a.server.store.Workspace()
	if limitStr != "" {
		limit, _ := strconv.Atoi(limitStr)
		if limit <= 0 {
			limit = 20
		}
		chats, nextCursor, err := a.server.store.ListChatsPaged(r.Context(), ws.ID, limit, cursor)
		if err != nil {
			return restruct.Error{Status: http.StatusInternalServerError, Err: err}
		}
		return map[string]any{"chats": chats, "cursor": nextCursor}
	}

	// Legacy: return all chats
	chats, err := a.server.store.ListChats(r.Context(), ws.ID)
	if err != nil {
		return restruct.Error{Status: http.StatusInternalServerError, Err: err}
	}
	return chats
}

// Chats_0 handles operations on a specific chat (GET messages, DELETE).
func (a *Api) Chats_0(r *http.Request) any {
	chatID, _ := strconv.ParseInt(restruct.Params(r)["0"], 10, 64)
	if chatID == 0 {
		return restruct.Error{Status: http.StatusBadRequest, Message: "valid chat id required"}
	}
	wsID := a.server.store.Workspace().ID
	if r.Method == http.MethodDelete {
		// Remove in-memory session
		a.server.mu.Lock()
		delete(a.server.chats, chatID)
		a.server.mu.Unlock()
		// Delete from store (cascades to messages + history)
		if err := a.server.store.DeleteChat(r.Context(), &config.Chat{ID: chatID, Workspace: wsID}); err != nil {
			return restruct.Error{Status: http.StatusInternalServerError, Err: err}
		}
		// Clear in-memory cron references to this chat
		if a.server.cronMgr != nil {
			a.server.cronMgr.ClearChatID(chatID)
		}
		return map[string]string{"status": "deleted"}
	}

	if r.Method == http.MethodGet {
		limitStr := r.URL.Query().Get("limit")
		cursor := r.URL.Query().Get("cursor")

		// Paginated mode when limit is provided
		if limitStr != "" {
			limit, _ := strconv.Atoi(limitStr)
			if limit <= 0 {
				limit = 20
			}
			chat := &config.Chat{ID: chatID, Workspace: wsID}
			msgs, nextCursor, err := a.server.store.ListChatMessagesPaged(r.Context(), chat, limit, cursor)
			if err != nil {
				return restruct.Error{Status: http.StatusInternalServerError, Err: err}
			}
			// Reverse to chronological order (DB returns newest-first)
			for i, j := 0, len(msgs)-1; i < j; i, j = i+1, j-1 {
				msgs[i], msgs[j] = msgs[j], msgs[i]
			}
			return map[string]any{"messages": msgs, "cursor": nextCursor}
		}

		// Legacy: return all messages (no pagination)
		msgs, err := a.server.store.ListChatMessages(r.Context(), &config.Chat{ID: chatID, Workspace: wsID})
		if err != nil {
			return restruct.Error{Status: http.StatusInternalServerError, Err: err}
		}
		return msgs
	}
	return nil
}

// ChatStatus_0 returns the running state of a chat and any buffered events.
func (a *Api) ChatStatus_0(r *http.Request) any {
	chatID, _ := strconv.ParseInt(restruct.Params(r)["0"], 10, 64)
	if chatID == 0 {
		return restruct.Error{Status: http.StatusBadRequest, Message: "valid chat id required"}
	}
	a.server.mu.Lock()
	sess, ok := a.server.chats[chatID]
	a.server.mu.Unlock()
	running := false
	var events []chatEvent
	if ok {
		sess.mu.Lock()
		running = sess.running
		if running && len(sess.pendingEvents) > 0 {
			events = make([]chatEvent, len(sess.pendingEvents))
			copy(events, sess.pendingEvents)
		}
		sess.mu.Unlock()
	}
	return map[string]any{"running": running, "events": events}
}

// Chats_0_Answer handles answering a ui.ask prompt.
func (a *Api) Chats_0_Answer(r *http.Request) any {
	chatID, _ := strconv.ParseInt(restruct.Params(r)["0"], 10, 64)
	if chatID == 0 {
		return restruct.Error{Status: http.StatusBadRequest, Message: "valid chat id required"}
	}
	var req struct {
		Answer string `json:"answer"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return restruct.Error{Status: http.StatusBadRequest, Message: "invalid json"}
	}

	a.server.mu.Lock()
	sess, ok := a.server.chats[chatID]
	a.server.mu.Unlock()

	if !ok {
		return restruct.Error{Status: http.StatusNotFound, Message: "chat session not found"}
	}

	sess.mu.Lock()
	running := sess.running
	sess.mu.Unlock()

	if !running {
		return restruct.Error{Status: http.StatusConflict, Message: "chat is not running"}
	}

	// Non-blocking send
	select {
	case sess.askChan <- req.Answer:
		return map[string]string{"status": "answered"}
	default:
		return restruct.Error{Status: http.StatusConflict, Message: "agent is not waiting for an answer"}
	}
}

// Chats_0_Inject queues a user message to be injected into the next agent iteration.
// The message is persisted immediately and will be picked up by the agent at the start
// of its next loop iteration.
func (a *Api) Chats_0_Inject(r *http.Request) any {
	chatID, _ := strconv.ParseInt(restruct.Params(r)["0"], 10, 64)
	if chatID == 0 {
		return restruct.Error{Status: http.StatusBadRequest, Message: "valid chat id required"}
	}
	var req struct {
		Message string `json:"message"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Message == "" {
		return restruct.Error{Status: http.StatusBadRequest, Message: "message required"}
	}

	a.server.mu.Lock()
	sess, ok := a.server.chats[chatID]
	a.server.mu.Unlock()

	if !ok {
		return restruct.Error{Status: http.StatusNotFound, Message: "chat session not found"}
	}

	sess.mu.Lock()
	running := sess.running
	if running {
		sess.pendingUserMsgs = append(sess.pendingUserMsgs, req.Message)
	}
	sess.mu.Unlock()

	if !running {
		return restruct.Error{Status: http.StatusConflict, Message: "chat is not running"}
	}

	// Persist the user message immediately so it appears in correct order on page refresh
	wsID := a.server.store.Workspace().ID
	_ = a.server.store.AddChatMessage(r.Context(), &config.Chat{ID: chatID, Workspace: wsID}, "user", req.Message, "", 0, 0)

	// Broadcast so other tabs can show the pending message
	sess.bufferAndBroadcast(a.server.hub, chatEvent{Type: "pending_msg", Content: req.Message, ChatID: chatID})

	return map[string]string{"status": "queued"}
}
