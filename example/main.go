package main

import (
	"crypto/sha256"
	"errors"
	"fmt"

	"golang.org/x/time/rate"

	"github.com/acrmp/mcp"
)

func main() {
	serverInfo := mcp.Implementation{
		Name:    "ExampleServer",
		Version: "1.0.0",
	}
	desc := "Compute a SHA-256 checksum"
	tools := []mcp.ToolDefinition{
		{
			Metadata: mcp.Tool{
				Name:        "sha256sum",
				Description: &desc,
				InputSchema: mcp.ToolInputSchema{
					Type: "object",
					Properties: mcp.ToolInputSchemaProperties{
						"text": map[string]any{
							"type":        "string",
							"description": "Text to compute a checksum for",
						},
					},
				},
			},
			Execute:   computeSHA256,
			RateLimit: rate.NewLimiter(10, 1),
		},
	}

	s := mcp.NewServer(serverInfo, tools)
	s.Serve()
}

func computeSHA256(params mcp.CallToolRequestParams) (mcp.CallToolResult, error) {
	txt := params.Arguments["text"].(string)

	if len(txt) == 0 {
		return mcp.CallToolResult{}, errors.New("failed to compute checksum: text cannot be empty")
	}

	h := sha256.New()
	h.Write([]byte(txt))

	checksum := fmt.Sprintf("%x", h.Sum(nil))
	var noError bool
	return mcp.CallToolResult{
		Content: []any{
			mcp.TextContent{
				Type: "text",
				Text: checksum,
			},
		},
		IsError: &noError,
	}, nil
}
