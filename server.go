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

type ToolDefinition struct {
	Metadata  Tool
	Execute   func(CallToolRequestParams) (CallToolResult, error)
	RateLimit *rate.Limiter
}

type handler struct {
	serverInfo   Implementation
	toolMetadata []Tool
	tools        map[string]ToolDefinition
}

type Server struct {
	handler *handler
}

func NewServer(serverInfo Implementation, tools []ToolDefinition) *Server {
	toolMetadata := make([]Tool, 0, len(tools))
	toolFuncs := make(map[string]ToolDefinition, len(tools))
	for _, t := range tools {
		toolMetadata = append(toolMetadata, t.Metadata)
		toolFuncs[t.Metadata.Name] = t
	}
	return &Server{handler: &handler{serverInfo: serverInfo, toolMetadata: toolMetadata, tools: toolFuncs}}
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

	response, err := t.Execute(params)
	if err != nil {
		h.replyWithToolError(ctx, conn, req, err.Error())
		return
	}

	h.replyWithResult(ctx, conn, req, response)
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
