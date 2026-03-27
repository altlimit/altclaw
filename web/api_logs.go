package web

import (
	"net/http"
	"strconv"
	"strings"

	"altclaw.ai/internal/bridge"
)

// Logs returns recent log entries from the in-memory ring buffer.
// Accepts optional query params:
//
//	?limit=N     — number of entries (default 100, max 500)
//	?query=...   — keyword search across messages and attrs
//	?level=...   — comma-separated levels to include (e.g. "WARN,ERROR")
func (a *Api) Logs(r *http.Request) any {
	buf := a.server.logBuf
	if buf == nil {
		return map[string]any{"entries": []any{}, "total": 0}
	}

	limit := 100
	if l, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && l > 0 {
		if l > 500 {
			l = 500
		}
		limit = l
	}

	query := r.URL.Query().Get("query")
	levelFilter := r.URL.Query().Get("level")

	// Build allowed-levels set
	var allowedLevels map[string]bool
	if levelFilter != "" {
		allowedLevels = make(map[string]bool)
		for _, lv := range strings.Split(levelFilter, ",") {
			allowedLevels[strings.TrimSpace(strings.ToUpper(lv))] = true
		}
	}

	// Fetch entries — either via search or recent
	var rawEntries []map[string]interface{}
	if query != "" {
		entries := buf.Search(query)
		for _, e := range entries {
			rawEntries = append(rawEntries, bridge.EntryToMap(e))
		}
	} else {
		entries := buf.Recent(0) // all entries
		for _, e := range entries {
			rawEntries = append(rawEntries, bridge.EntryToMap(e))
		}
	}

	// Apply level filter
	var filtered []map[string]interface{}
	for _, m := range rawEntries {
		if allowedLevels != nil {
			level, _ := m["level"].(string)
			if !allowedLevels[level] {
				continue
			}
		}
		filtered = append(filtered, m)
	}

	total := len(filtered)
	if limit < total {
		filtered = filtered[:limit]
	}

	if filtered == nil {
		filtered = []map[string]interface{}{}
	}

	return map[string]any{
		"entries": filtered,
		"total":   total,
	}
}
