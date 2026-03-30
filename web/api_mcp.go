package web

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/altlimit/restruct"
)

// Routes registers custom routes that can't be expressed via restruct's
// default method-name-to-path convention.
func (s *Server) Routes() []restruct.Route {
	return []restruct.Route{
		{Handler: "WellKnownMcp", Path: ".well-known/mcp.json", Methods: []string{http.MethodGet}},
	}
}

// Mcp handles JSON-RPC 2.0 MCP requests for the root namespace.
// POST /mcp — public endpoint, no auth required.
// Tools are loaded from .agent/mcp/*.js files in the workspace.
func (s *Server) Mcp(w http.ResponseWriter, r *http.Request) {
	s.serveMcp(w, r, "")
}

// McpNs handles JSON-RPC 2.0 MCP requests for a sub-namespace.
// POST /mcp/{namespace} — public endpoint, no auth required.
// Tools are loaded from .agent/mcp/{namespace}/*.js files.
func (s *Server) Mcp_0(w http.ResponseWriter, r *http.Request) {
	namespace := restruct.Params(r)["0"]
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	s.serveMcp(w, r, namespace)
}

// serveMcp is the shared handler for root and namespaced MCP requests.
func (s *Server) serveMcp(w http.ResponseWriter, r *http.Request, namespace string) {
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

	resp := s.mcpServer.HandleRequest(body, namespace)
	if resp == nil {
		// Notification — no response needed
		w.WriteHeader(http.StatusNoContent)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write(resp)
}

// WellKnownMcp serves the MCP discovery manifest at /.well-known/mcp.json.
// Lists all available MCP endpoints (root + sub-namespaces).
func (s *Server) WellKnownMcp(w http.ResponseWriter, r *http.Request) {
	if s.mcpServer == nil || !s.mcpServer.HasAnyTools() {
		http.Error(w, "Not found", http.StatusNotFound)
		return
	}

	var endpoints []map[string]any

	// Root endpoint
	if s.mcpServer.HasTools("") {
		entry := map[string]any{
			"transport": map[string]any{
				"type":     "http",
				"endpoint": "/mcp",
			},
		}
		if m := s.mcpServer.ReadManifest(""); m.Description != "" {
			entry["description"] = m.Description
		}
		endpoints = append(endpoints, entry)
	}

	// Sub-namespace endpoints
	for _, ns := range s.mcpServer.ListNamespaces() {
		entry := map[string]any{
			"transport": map[string]any{
				"type":     "http",
				"endpoint": "/mcp/" + ns,
			},
		}
		if m := s.mcpServer.ReadManifest(ns); m.Description != "" {
			entry["description"] = m.Description
		}
		endpoints = append(endpoints, entry)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"endpoints": endpoints,
	})
}
