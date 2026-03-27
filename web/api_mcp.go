package web

import (
	"io"
	"net/http"
)

// Mcp handles JSON-RPC 2.0 MCP requests.
// POST /mcp — public endpoint, no auth required.
// Tools are user-curated via .altclaw/mcp/*.js files in the workspace.
func (s *Server) Mcp(w http.ResponseWriter, r *http.Request) {
	if s.mcpServer == nil {
		http.Error(w, "MCP not configured", http.StatusServiceUnavailable)
		return
	}

	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read body", http.StatusBadRequest)
		return
	}

	resp := s.mcpServer.HandleRequest(body)
	if resp == nil {
		// Notification — no response needed
		w.WriteHeader(http.StatusNoContent)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write(resp)
}
