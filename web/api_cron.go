package web

import (
	"net/http"
	"strconv"

	"altclaw.ai/internal/cron"
	"github.com/altlimit/restruct"
)

// CronJobs returns the list of active cron jobs.
func (a *Api) CronJobs(r *http.Request) any {
	mgr := a.server.cronMgr
	if mgr == nil {
		return []cron.JobInfo{}
	}
	if r.Method == http.MethodDelete {
		return restruct.Error{Status: http.StatusMethodNotAllowed, Message: "use /api/cron-jobs/:id"}
	}
	return mgr.List()
}

// CronJobs_0 handles DELETE /api/cron-jobs/:id to remove a cron job.
func (a *Api) CronJobs_0(r *http.Request) any {
	mgr := a.server.cronMgr
	if mgr == nil {
		return restruct.Error{Status: http.StatusNotFound, Message: "cron not available"}
	}

	idStr := restruct.Params(r)["0"]
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		return restruct.Error{Status: http.StatusBadRequest, Message: "invalid job ID"}
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
