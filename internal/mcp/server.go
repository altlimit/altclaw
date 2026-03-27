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
// Tools are loaded from {workspace}/.altclaw/mcp/*.js files.
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

// HandleRequest processes a JSON-RPC 2.0 request and returns a response.
func (s *Server) HandleRequest(data []byte) []byte {
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
		response = s.handleInitialize(req)
	case "notifications/initialized":
		// Client acknowledgement — no response needed for notifications
		return nil
	case "ping":
		response = newResponse(req.ID, map[string]any{})
	case "tools/list":
		response = s.handleToolsList(req)
	case "tools/call":
		response = s.handleToolsCall(req)
	default:
		response = newError(req.ID, CodeMethodNotFound, fmt.Sprintf("Method not found: %s", req.Method))
	}

	resp, _ := json.Marshal(response)
	return resp
}

// handleInitialize processes the initialize request.
func (s *Server) handleInitialize(req *Request) *Response {
	return newResponse(req.ID, map[string]any{
		"protocolVersion": protocolVersion,
		"capabilities": map[string]any{
			"tools": map[string]any{},
		},
		"serverInfo": map[string]any{
			"name":    "altclaw",
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

// handleToolsList returns the list of available tools from .altclaw/mcp/*.js.
func (s *Server) handleToolsList(req *Request) *Response {
	tools := s.scanTools()
	return newResponse(req.ID, map[string]any{
		"tools": tools,
	})
}

// handleToolsCall executes a tool by loading and running the corresponding .altclaw/mcp/*.js file.
func (s *Server) handleToolsCall(req *Request) *Response {
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
	toolDir := filepath.Join(s.ws.Path, ".altclaw", "mcp")
	toolFile := filepath.Join(toolDir, params.Name+".js")

	// Security: ensure the resolved path is inside .altclaw/mcp/
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
	result, execErr := s.executeTool(params.Name, params.Arguments)
	if execErr != nil {
		slog.Error("mcp tool error", "tool", params.Name, "error", execErr)
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

// executeTool loads a .altclaw/mcp/*.js file via require() and calls it with the given arguments.
// Uses RunModule for consistent CommonJS handling.
func (s *Server) executeTool(toolName string, argsJSON json.RawMessage) (string, error) {
	handler := &mcpUI{}

	eng := engine.New(s.ws, s.exec, handler, "", s.store)
	defer eng.Cleanup()

	argsStr := "{}"
	if len(argsJSON) > 0 {
		argsStr = string(argsJSON)
	}

	// Inline module that requires the tool and calls it with params.
	// RunModule handles the CommonJS wrapping and calls module.exports if it's a function.
	code := fmt.Sprintf(`var __params = %s;
var __fn = require('./.altclaw/mcp/%s.js');
module.exports = function() {
  if (typeof __fn === 'function') {
    var r = __fn(__params);
    return typeof r === 'object' ? JSON.stringify(r) : String(r || '');
  }
  return '';
};`, argsStr, toolName)

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

// scanTools reads .altclaw/mcp/*.js and returns tool definitions.
func (s *Server) scanTools() []ToolDef {
	toolDir := filepath.Join(s.ws.Path, ".altclaw", "mcp")
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
