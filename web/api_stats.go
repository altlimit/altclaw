package web

import (
	"context"
)

// Stats returns lightweight workspace summary for the hub dashboard.
// This endpoint is exempted from auth so the hub can proxy it through the tunnel.
func (a *Api) Stats() any {
	ctx := context.Background()

	wsID := a.server.store.Workspace().ID
	// Count chats
	chats, _ := a.server.store.ListChats(ctx, wsID)
	chatCount := len(chats)

	// Count cron jobs
	var cronCount int
	if a.server.cronMgr != nil {
		cronCount = len(a.server.cronMgr.List())
	}

	// Count active (running) chat sessions
	a.server.mu.Lock()
	var activeChats int
	for _, sess := range a.server.chats {
		sess.mu.Lock()
		if sess.running {
			activeChats++
		}
		sess.mu.Unlock()
	}
	a.server.mu.Unlock()

	tokenUsageToday, _ := a.server.store.TodayTokenUsage(ctx, wsID)
	tokenUsageHistory, _ := a.server.store.GetTokenUsage(ctx, wsID, 14)

	return map[string]any{
		"chats":               chatCount,
		"cron_jobs":           cronCount,
		"active_chats":        activeChats,
		"token_usage_today":   tokenUsageToday,
		"token_usage_history": tokenUsageHistory,
	}
}
