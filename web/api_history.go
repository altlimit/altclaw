package web

import (
	"context"
	"net/http"
	"strconv"

	"altclaw.ai/internal/config"
	"github.com/altlimit/restruct"
)

// History_0 lists all history entries for a specific chat.
func (a *Api) History_0(r *http.Request) any {
	chatID, _ := strconv.ParseInt(restruct.Params(r)["0"], 10, 64)
	if chatID == 0 {
		return restruct.Error{Status: http.StatusBadRequest, Message: "valid chat id required"}
	}

	chat := &config.Chat{ID: chatID, Workspace: a.server.store.Workspace().ID}
	entries, err := a.server.store.ListHistoryByChat(r.Context(), chat)
	if err != nil {
		return restruct.Error{Status: http.StatusInternalServerError, Err: err}
	}
	return entries
}

// History_0_1 lists history entries for a specific turn within a chat.
func (a *Api) History_0_1(r *http.Request) any {
	chatID, _ := strconv.ParseInt(restruct.Params(r)["0"], 10, 64)
	turnID := restruct.Params(r)["1"]
	if chatID == 0 || turnID == "" {
		return restruct.Error{Status: http.StatusBadRequest, Message: "valid chat id and turn id required"}
	}

	chat := &config.Chat{ID: chatID, Workspace: a.server.store.Workspace().ID}
	entries, err := a.server.store.ListHistoryByTurn(r.Context(), chat, turnID)
	if err != nil {
		return restruct.Error{Status: http.StatusInternalServerError, Err: err}
	}
	return entries
}

// History returns all history entries for the current workspace.
func (a *Api) History(ctx context.Context) any {
	entries, err := a.server.store.ListHistory(ctx, a.server.store.Workspace().ID)
	if err != nil {
		return restruct.Error{Status: http.StatusInternalServerError, Err: err}
	}
	return entries
}
