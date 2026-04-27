package web

import (
	"net/http"
	"strconv"

	"github.com/altlimit/restruct"
)

// Connections returns the list of active persistent connections.
func (a *Api) Connections(r *http.Request) any {
	mgr := a.server.connMgr
	if mgr == nil {
		return []any{}
	}
	if r.Method == http.MethodDelete {
		return restruct.Error{Status: http.StatusMethodNotAllowed, Message: "use /api/connections/:id"}
	}
	return mgr.List()
}

// Connections_0 handles DELETE /api/connections/:id to close and remove a connection.
func (a *Api) Connections_0(r *http.Request) any {
	mgr := a.server.connMgr
	if mgr == nil {
		return restruct.Error{Status: http.StatusNotFound, Message: "connections not available"}
	}

	idStr := restruct.Params(r)["0"]
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		return restruct.Error{Status: http.StatusBadRequest, Message: "invalid connection ID"}
	}

	if r.Method == http.MethodDelete {
		ctx := r.Context()
		if err := mgr.Remove(ctx, id); err != nil {
			return restruct.Error{Status: http.StatusInternalServerError, Message: err.Error()}
		}
		return map[string]string{"status": "deleted"}
	}

	return restruct.Error{Status: http.StatusMethodNotAllowed}
}
