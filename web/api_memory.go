package web

import (
	"context"
	"net/http"
	"strconv"

	"altclaw.ai/internal/config"
	"github.com/altlimit/restruct"
)

// MemoryEntries returns paginated memory entries (workspace + user scoped).
// Accepts optional query params: ?limit=N&ws_cursor=X&user_cursor=X
func (a *Api) MemoryEntries(r *http.Request) any {
	limit := 20
	if l, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && l > 0 && l <= 100 {
		limit = l
	}
	wsCursor := r.URL.Query().Get("ws_cursor")
	userCursor := r.URL.Query().Get("user_cursor")
	wsID := a.server.store.Workspace().ID

	wsEntries, nextWsCursor, _ := a.server.store.ListMemoryEntriesPaged(r.Context(), wsID, limit, wsCursor)
	userEntries, nextUserCursor, _ := a.server.store.ListMemoryEntriesPaged(r.Context(), "", limit, userCursor)
	if len(userEntries) < limit {
		nextUserCursor = ""
	}
	if len(wsEntries) < limit {
		nextWsCursor = ""
	}

	if wsEntries == nil {
		wsEntries = []*config.Memory{}
	}
	if userEntries == nil {
		userEntries = []*config.Memory{}
	}
	return map[string]any{
		"workspace":   wsEntries,
		"user":        userEntries,
		"ws_cursor":   nextWsCursor,
		"user_cursor": nextUserCursor,
	}
}

// MemoryEntries_0 handles GET/DELETE /api/memory-entries/:id.
func (a *Api) MemoryEntries_0(r *http.Request) any {
	idStr := restruct.Params(r)["0"]
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		return restruct.Error{Status: http.StatusBadRequest, Message: "invalid entry ID"}
	}

	ctx := r.Context()
	wsID := a.server.store.Workspace().ID

	if r.Method == http.MethodDelete {
		// Try workspace first, then user-level
		if err := a.server.store.DeleteMemory(ctx, wsID, id); err != nil {
			if err := a.server.store.DeleteMemory(ctx, "", id); err != nil {
				return restruct.Error{Status: http.StatusNotFound, Message: "entry not found"}
			}
		}
		return map[string]string{"status": "deleted"}
	}

	// GET single entry
	entry, err := a.server.store.GetMemory(ctx, wsID, id)
	if err != nil {
		entry, err = a.server.store.GetMemory(ctx, "", id)
		if err != nil {
			return restruct.Error{Status: http.StatusNotFound, Message: "entry not found"}
		}
	}
	return entry
}

// SaveMemoryEntry creates or updates a memory entry.
func (a *Api) SaveMemoryEntry(ctx context.Context, req struct {
	ID      int64  `json:"id"`
	Kind    string `json:"kind"`
	Content string `json:"content"`
	Scope   string `json:"scope"` // "workspace" or "user"
}) any {
	wsNS := ""
	if req.Scope == "workspace" {
		wsNS = a.server.store.Workspace().ID
	}

	if req.Kind == "" {
		req.Kind = "learned"
	}

	if req.ID > 0 {
		// Update existing
		entry, err := a.server.store.GetMemory(ctx, wsNS, req.ID)
		if err != nil {
			return restruct.Error{Status: http.StatusNotFound, Message: "entry not found"}
		}
		entry.Content = req.Content
		if req.Kind != "" {
			entry.Kind = req.Kind
		}
		if err := a.server.store.UpdateMemory(ctx, entry); err != nil {
			return restruct.Error{Status: http.StatusInternalServerError, Err: err}
		}
		return map[string]any{"status": "updated", "id": entry.ID}
	}

	// Create new
	entry := &config.Memory{
		Workspace: wsNS,
		Kind:      req.Kind,
		Content:   req.Content,
	}
	if err := a.server.store.AddMemory(ctx, entry); err != nil {
		return restruct.Error{Status: http.StatusInternalServerError, Err: err}
	}
	return map[string]any{"status": "created", "id": entry.ID}
}
