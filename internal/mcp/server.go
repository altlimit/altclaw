package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"altclaw.ai/internal/config"
	"altclaw.ai/internal/engine"
	"altclaw.ai/internal/executor"
)

// Protocol version we implement.
const protocolVersion = "2025-03-26"

// Server implements the MCP server protocol.
// Tools are loaded from {workspace}/.agent/mcp/*.js files (root namespace)
// and {workspace}/.agent/mcp/{ns}/*.js files (sub-namespaces).
type Server struct {
	ws    *config.Workspace
	store *config.Store
	exec  executor.Executor
}

// NewServer creates a new MCP server.
func NewServer(ws *config.Workspace, store *config.Store, exec executor.Executor) *Server {
	return &Server{
		ws:    ws,
		store: store,
		exec:  exec,
	}
}

// HasTools returns true if there are any tools in the given namespace.
// Empty namespace checks the root .agent/mcp/*.js directory.
func (s *Server) HasTools(namespace string) bool {
	return len(s.scanTools(namespace)) > 0
}

// HasAnyTools returns true if there are tools in any namespace (root or sub).
func (s *Server) HasAnyTools() bool {
	if s.HasTools("") {
		return true
	}
	return len(s.ListNamespaces()) > 0
}

// ListNamespaces returns subdirectory names under .agent/mcp/ that contain
// at least one .js tool file. Does not include the root namespace.
func (s *Server) ListNamespaces() []string {
	mcpDir := filepath.Join(s.ws.Path, ".agent", "mcp")
	entries, err := os.ReadDir(mcpDir)
	if err != nil {
		return nil
	}

	var namespaces []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		// Check if the subdirectory has any .js files
		subEntries, err := os.ReadDir(filepath.Join(mcpDir, e.Name()))
		if err != nil {
			continue
		}
		for _, se := range subEntries {
			if !se.IsDir() && strings.HasSuffix(se.Name(), ".js") {
				namespaces = append(namespaces, e.Name())
				break
			}
		}
	}
	return namespaces
}

// Manifest holds metadata from a mcp.json file in a tool directory.
type Manifest struct {
	Description string `json:"description,omitempty"`
}

// ReadManifest reads the manifest.json file from the given namespace's tool directory.
// Returns an empty Manifest if the file doesn't exist or is invalid.
func (s *Server) ReadManifest(namespace string) Manifest {
	data, err := os.ReadFile(filepath.Join(s.toolDir(namespace), "manifest.json"))
	if err != nil {
		return Manifest{}
	}
	var m Manifest
	json.Unmarshal(data, &m)
	return m
}

// HandleRequest processes a JSON-RPC 2.0 request and returns a response.
// The namespace parameter selects which tool directory to use:
// "" for root (.agent/mcp/*.js), "calculator" for .agent/mcp/calculator/*.js.
func (s *Server) HandleRequest(data []byte, namespace string) []byte {
	req, err := parseRequest(data)
	if err != nil {
		resp, _ := json.Marshal(newError(nil, CodeParseError, "Parse error"))
		return resp
	}

	if req.JSONRPC != "2.0" {
		resp, _ := json.Marshal(newError(req.ID, CodeInvalidRequest, "Invalid JSON-RPC version"))
		return resp
	}

	var response *Response
	switch req.Method {
	case "initialize":
		response = s.handleInitialize(req, namespace)
	case "notifications/initialized":
		// Client acknowledgement — no response needed for notifications
		return nil
	case "ping":
		response = newResponse(req.ID, map[string]any{})
	case "tools/list":
		response = s.handleToolsList(req, namespace)
	case "tools/call":
		response = s.handleToolsCall(req, namespace)
	default:
		response = newError(req.ID, CodeMethodNotFound, fmt.Sprintf("Method not found: %s", req.Method))
	}

	resp, _ := json.Marshal(response)
	return resp
}

// handleInitialize processes the initialize request.
func (s *Server) handleInitialize(req *Request, namespace string) *Response {
	name := "altclaw"
	if namespace != "" {
		name = "altclaw/" + namespace
	}
	return newResponse(req.ID, map[string]any{
		"protocolVersion": protocolVersion,
		"capabilities": map[string]any{
			"tools": map[string]any{},
		},
		"serverInfo": map[string]any{
			"name":    name,
			"version": "0.1.0",
		},
	})
}

// ToolDef is a single MCP tool definition.
type ToolDef struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	InputSchema any    `json:"inputSchema"`
}

// handleToolsList returns the list of available tools for the given namespace.
func (s *Server) handleToolsList(req *Request, namespace string) *Response {
	tools := s.scanTools(namespace)
	return newResponse(req.ID, map[string]any{
		"tools": tools,
	})
}

// handleToolsCall executes a tool from the given namespace.
func (s *Server) handleToolsCall(req *Request, namespace string) *Response {
	var params struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments,omitempty"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return newError(req.ID, CodeInvalidParams, "Invalid params: "+err.Error())
	}

	if params.Name == "" {
		return newError(req.ID, CodeInvalidParams, "Tool name is required")
	}

	// Verify the tool file exists
	toolDir := s.toolDir(namespace)
	toolFile := filepath.Join(toolDir, params.Name+".js")

	// Security: ensure the resolved path is inside the tool directory
	realToolDir, _ := filepath.EvalSymlinks(toolDir)
	realToolFile, err := filepath.EvalSymlinks(toolFile)
	if err != nil {
		realToolFile = toolFile
	}
	if realToolDir == "" {
		realToolDir = toolDir
	}
	rel, err := filepath.Rel(realToolDir, realToolFile)
	if err != nil || strings.HasPrefix(rel, "..") {
		return newError(req.ID, CodeInvalidParams, fmt.Sprintf("Tool not found: %s", params.Name))
	}

	if _, err := os.Stat(toolFile); os.IsNotExist(err) {
		return newError(req.ID, CodeInvalidParams, fmt.Sprintf("Tool not found: %s", params.Name))
	}

	// Execute the tool via RunModule
	result, execErr := s.executeTool(namespace, params.Name, params.Arguments)
	if execErr != nil {
		slog.Error("mcp tool error", "tool", params.Name, "namespace", namespace, "error", execErr)
		return newResponse(req.ID, map[string]any{
			"content": []map[string]any{
				{"type": "text", "text": fmt.Sprintf("Error: %v", execErr)},
			},
			"isError": true,
		})
	}

	return newResponse(req.ID, map[string]any{
		"content": []map[string]any{
			{"type": "text", "text": result},
		},
	})
}

// toolDir returns the absolute path to the tool directory for the given namespace.
func (s *Server) toolDir(namespace string) string {
	if namespace == "" {
		return filepath.Join(s.ws.Path, ".agent", "mcp")
	}
	return filepath.Join(s.ws.Path, ".agent", "mcp", namespace)
}

// toolRequirePath returns the require() path for a tool in the given namespace.
func (s *Server) toolRequirePath(namespace, toolName string) string {
	if namespace == "" {
		return fmt.Sprintf("./.agent/mcp/%s.js", toolName)
	}
	return fmt.Sprintf("./.agent/mcp/%s/%s.js", namespace, toolName)
}

// executeTool loads a tool .js file via require() and calls it with the given arguments.
// Uses RunModule for consistent CommonJS handling.
func (s *Server) executeTool(namespace, toolName string, argsJSON json.RawMessage) (string, error) {
	handler := &mcpUI{}

	eng := engine.New(s.ws, s.exec, handler, "", s.store)
	defer eng.Cleanup()

	argsStr := "{}"
	if len(argsJSON) > 0 {
		argsStr = string(argsJSON)
	}

	requirePath := s.toolRequirePath(namespace, toolName)

	// Inline module that requires the tool and calls it with params.
	// RunModule handles the CommonJS wrapping and calls module.exports if it's a function.
	code := fmt.Sprintf(`var __params = %s;
var __fn = require('%s');
module.exports = function() {
  if (typeof __fn === 'function') {
    var r = __fn(__params);
    return typeof r === 'object' ? JSON.stringify(r) : String(r || '');
  }
  return '';
};`, argsStr, requirePath)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	result := eng.RunModule(ctx, code)
	if result.Error != nil {
		return "", result.Error
	}

	return result.Value, nil
}

var metaPattern = regexp.MustCompile(`@(\w+)\s+([^@*/]+)`)
var schemaPattern = regexp.MustCompile(`(?m)^//\s*inputSchema:\s*(.+)$`)

// scanTools reads *.js files from the tool directory for the given namespace.
func (s *Server) scanTools(namespace string) []ToolDef {
	toolDir := s.toolDir(namespace)
	entries, err := os.ReadDir(toolDir)
	if err != nil {
		return nil
	}

	var tools []ToolDef
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".js") {
			continue
		}

		data, err := os.ReadFile(filepath.Join(toolDir, e.Name()))
		if err != nil {
			continue
		}
		src := string(data)

		tool := ToolDef{
			Name: strings.TrimSuffix(e.Name(), ".js"),
		}

		// Parse @name and @description from /** ... */ block
		if idx := strings.Index(src, "/**"); idx >= 0 {
			if end := strings.Index(src[idx:], "*/"); end >= 0 {
				block := src[idx : idx+end+2]
				for _, match := range metaPattern.FindAllStringSubmatch(block, -1) {
					switch match[1] {
					case "name":
						tool.Name = strings.TrimSpace(match[2])
					case "description":
						tool.Description = strings.TrimSpace(match[2])
					}
				}
			}
		}

		// Parse // inputSchema: {...} line
		if match := schemaPattern.FindStringSubmatch(src); len(match) > 1 {
			var schema any
			if err := json.Unmarshal([]byte(match[1]), &schema); err == nil {
				tool.InputSchema = schema
			}
		}

		// Default inputSchema if none specified
		if tool.InputSchema == nil {
			tool.InputSchema = map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			}
		}

		tools = append(tools, tool)
	}

	return tools
}

// mcpUI is a headless UI handler for MCP tool execution.
type mcpUI struct{}

func (m *mcpUI) Log(msg string)      { slog.Info("mcp", "log", msg) }
func (m *mcpUI) Ask(q string) string { return "" }
func (m *mcpUI) Confirm(action, label, summary string, params map[string]any) string {
	return "no"
}
