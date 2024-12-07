package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"

	"github.com/sourcegraph/jsonrpc2"
	"golang.org/x/time/rate"
)

const SupportedProtocolVersion = "2024-11-05"

// Notifier provides a method for sending MCP notifications
type Notifier interface {
	Notify(ctx context.Context, method string, params any) error
}

// connNotifier implements Notifier using a jsonrpc2.Conn
type connNotifier struct{ *jsonrpc2.Conn }

func (n *connNotifier) Notify(ctx context.Context, method string, params any) error {
	return n.Conn.Notify(ctx, method, params)
}

type ToolDefinition struct {
	Metadata  Tool
	Execute   func(context.Context, Notifier, CallToolRequestParams) (CallToolResult, error)
	RateLimit *rate.Limiter
}

type PromptDefinition struct {
	Metadata  Prompt
	Process   func(context.Context, Notifier, GetPromptRequestParams) (GetPromptResult, error)
	RateLimit *rate.Limiter
}

type handler struct {
	serverInfo     Implementation
	toolMetadata   []Tool
	tools          map[string]ToolDefinition
	promptMetadata []Prompt
	prompts        map[string]PromptDefinition
}

type Server struct {
	handler *handler
}

func NewServer(serverInfo Implementation, tools []ToolDefinition, prompts []PromptDefinition) *Server {
	toolMetadata := make([]Tool, 0, len(tools))
	toolFuncs := make(map[string]ToolDefinition, len(tools))
	for _, t := range tools {
		toolMetadata = append(toolMetadata, t.Metadata)
		toolFuncs[t.Metadata.Name] = t
	}

	promptMetadata := make([]Prompt, 0, len(prompts))
	promptFuncs := make(map[string]PromptDefinition, len(prompts))
	for _, p := range prompts {
		promptMetadata = append(promptMetadata, p.Metadata)
		promptFuncs[p.Metadata.Name] = p
	}

	return &Server{handler: &handler{
		serverInfo:     serverInfo,
		toolMetadata:   toolMetadata,
		tools:          toolFuncs,
		promptMetadata: promptMetadata,
		prompts:        promptFuncs,
	}}
}

func (s *Server) Serve() {
	stream := jsonrpc2.NewPlainObjectStream(&stdinStdoutReadWriter{})
	conn := jsonrpc2.NewConn(context.Background(), stream, s.handler)
	<-conn.DisconnectNotify()
}

func (h *handler) Handle(ctx context.Context, conn *jsonrpc2.Conn, req *jsonrpc2.Request) {
	switch req.Method {
	case "initialize":
		h.handleInitialize(ctx, conn, req)
	case "notifications/initialized":
	case "ping":
		h.replyWithResult(ctx, conn, req, struct{}{})
	case "tools/list":
		h.handleListTools(ctx, conn, req)
	case "tools/call":
		h.handleToolCall(ctx, conn, req)
	case "prompts/list":
		h.handleListPrompts(ctx, conn, req)
	case "prompts/get":
		h.handleGetPrompt(ctx, conn, req)
	default:
		h.replyWithJSONRPCError(ctx, conn, req, &jsonrpc2.Error{
			Code:    jsonrpc2.CodeMethodNotFound,
			Message: "Method not found",
		})
	}
}

func (h *handler) handleInitialize(ctx context.Context, conn *jsonrpc2.Conn, req *jsonrpc2.Request) {
	var unsupported bool
	response := InitializeResult{
		ProtocolVersion: SupportedProtocolVersion,
		ServerInfo:      h.serverInfo,
		Capabilities: ServerCapabilities{
			Experimental: map[string]map[string]any{},
			Tools: &ServerCapabilitiesTools{
				ListChanged: &unsupported,
			},
			Prompts: &ServerCapabilitiesPrompts{
				ListChanged: &unsupported,
			},
		},
	}
	h.replyWithResult(ctx, conn, req, response)
}

func (h *handler) handleListTools(ctx context.Context, conn *jsonrpc2.Conn, req *jsonrpc2.Request) {
	var params ListToolsRequestParams
	if req.Params != nil {
		// cursors are not supported so any cursor provided is invalid
		if err := json.Unmarshal(*req.Params, &params); err != nil || params.Cursor != nil {
			h.replyWithJSONRPCError(ctx, conn, req, &jsonrpc2.Error{
				Code:    jsonrpc2.CodeInvalidParams,
				Message: "Invalid params",
			})
			return
		}
	}
	h.replyWithResult(ctx, conn, req, ListToolsResult{Tools: h.toolMetadata})
}

func (h *handler) handleToolCall(ctx context.Context, conn *jsonrpc2.Conn, req *jsonrpc2.Request) {
	var params CallToolRequestParams
	if err := json.Unmarshal(*req.Params, &params); err != nil {
		h.replyWithJSONRPCError(ctx, conn, req, &jsonrpc2.Error{
			Code:    jsonrpc2.CodeInvalidParams,
			Message: "Invalid params",
		})
		return
	}

	t, ok := h.tools[params.Name]
	if !ok {
		h.replyWithJSONRPCError(ctx, conn, req, &jsonrpc2.Error{
			Code:    jsonrpc2.CodeInvalidParams,
			Message: fmt.Sprintf("Unknown tool: %s", params.Name),
		})
		return
	}

	if !t.RateLimit.Allow() {
		h.replyWithToolError(ctx, conn, req, "rate limit exceeded")
		return
	}

	for _, rqd := range t.Metadata.InputSchema.Required {
		if _, ok := params.Arguments[rqd]; !ok {
			h.replyWithJSONRPCError(ctx, conn, req, &jsonrpc2.Error{
				Code:    jsonrpc2.CodeInvalidParams,
				Message: "Invalid params",
			})
			return
		}
	}

	notifier := &connNotifier{Conn: conn}
	response, err := t.Execute(ctx, notifier, params)
	if err != nil {
		h.replyWithToolError(ctx, conn, req, err.Error())
		return
	}

	h.replyWithResult(ctx, conn, req, response)
}

func (h *handler) handleListPrompts(ctx context.Context, conn *jsonrpc2.Conn, req *jsonrpc2.Request) {
	var params ListPromptsRequestParams
	if req.Params != nil {
		if err := json.Unmarshal(*req.Params, &params); err != nil || params.Cursor != nil {
			h.replyWithJSONRPCError(ctx, conn, req, &jsonrpc2.Error{
				Code:    jsonrpc2.CodeInvalidParams,
				Message: "Invalid params",
			})
			return
		}
	}
	h.replyWithResult(ctx, conn, req, ListPromptsResult{Prompts: h.promptMetadata})
}

func (h *handler) handleGetPrompt(ctx context.Context, conn *jsonrpc2.Conn, req *jsonrpc2.Request) {
	var params GetPromptRequestParams
	if err := json.Unmarshal(*req.Params, &params); err != nil {
		h.replyWithJSONRPCError(ctx, conn, req, &jsonrpc2.Error{
			Code:    jsonrpc2.CodeInvalidParams,
			Message: "Invalid params",
		})
		return
	}

	p, ok := h.prompts[params.Name]
	if !ok {
		h.replyWithJSONRPCError(ctx, conn, req, &jsonrpc2.Error{
			Code:    jsonrpc2.CodeInvalidParams,
			Message: fmt.Sprintf("Unknown prompt: %s", params.Name),
		})
		return
	}

	if !p.RateLimit.Allow() {
		h.replyWithPromptError(ctx, conn, req, "rate limit exceeded")
		return
	}

	for _, arg := range p.Metadata.Arguments {
		if arg.Required != nil && *arg.Required {
			if _, ok := params.Arguments[arg.Name]; !ok {
				h.replyWithJSONRPCError(ctx, conn, req, &jsonrpc2.Error{
					Code:    jsonrpc2.CodeInvalidParams,
					Message: fmt.Sprintf("Missing required argument: %s", arg.Name),
				})
				return
			}
		}
	}

	notifier := &connNotifier{Conn: conn}
	result, err := p.Process(ctx, notifier, params)
	if err != nil {
		h.replyWithPromptError(ctx, conn, req, err.Error())
		return
	}

	h.replyWithResult(ctx, conn, req, result)
}

func (h *handler) replyWithPromptError(ctx context.Context, conn *jsonrpc2.Conn, req *jsonrpc2.Request, errMsg string) {
	result := GetPromptResult{
		Messages: []PromptMessage{{
			Role: RoleAssistant,
			Content: TextContent{
				Type: "text",
				Text: errMsg,
			},
		}},
	}
	h.replyWithResult(ctx, conn, req, result)
}

func (h *handler) replyWithJSONRPCError(ctx context.Context, conn *jsonrpc2.Conn, req *jsonrpc2.Request, rpcErr *jsonrpc2.Error) {
	if err := conn.ReplyWithError(ctx, req.ID, rpcErr); err != nil {
		slog.Error("problem replying with error", "method", req.Method, "error", err)
	}
}

func (h *handler) replyWithResult(ctx context.Context, conn *jsonrpc2.Conn, req *jsonrpc2.Request, result any) {
	if err := conn.Reply(ctx, req.ID, result); err != nil {
		slog.Error("problem replying with result", "method", req.Method, "error", err)
	}
}

func (h *handler) replyWithToolError(ctx context.Context, conn *jsonrpc2.Conn, req *jsonrpc2.Request, errMsg string) {
	errorOccurred := true
	result := CallToolResult{
		Content: []any{TextContent{Type: "text", Text: errMsg}},
		IsError: &errorOccurred,
	}
	h.replyWithResult(ctx, conn, req, result)
}

type stdinStdoutReadWriter struct{}

func (s stdinStdoutReadWriter) Read(p []byte) (int, error) {
	return os.Stdin.Read(p)
}

func (s stdinStdoutReadWriter) Write(p []byte) (int, error) {
	return os.Stdout.Write(p)
}

func (s stdinStdoutReadWriter) Close() error {
	return nil
}
