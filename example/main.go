package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"time"

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
					Required: []string{"text"},
				},
			},
			Execute:   computeSHA256,
			RateLimit: rate.NewLimiter(10, 1),
		},
	}

	prompts := []mcp.PromptDefinition{
		{
			Metadata: mcp.Prompt{
				Name:        "example",
				Description: ptr("An example prompt template"),
				Arguments: []mcp.PromptArgument{
					{
						Name:        "text",
						Description: ptr("Text to process"),
						Required:    ptr(true),
					},
				},
			},
			Process:   processPrompt,
			RateLimit: rate.NewLimiter(rate.Every(time.Second), 5),
		},
	}

	s := mcp.NewServer(serverInfo, tools, prompts)
	s.Serve()
}

func ptr[T any](t T) *T {
	return &t
}

// Update the computeSHA256 function to send a single notification
func computeSHA256(ctx context.Context, n mcp.Notifier, params mcp.CallToolRequestParams) (mcp.CallToolResult, error) {
	txt := params.Arguments["text"].(string)

	if len(txt) == 0 {
		return mcp.CallToolResult{}, errors.New("failed to compute checksum: text cannot be empty")
	}

	err := n.Notify(ctx, "test/notification", map[string]any{
		"message": "Processing text",
	})
	if err != nil {
		fmt.Printf("Failed to send notification: %v\n", err)
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

func processPrompt(ctx context.Context, n mcp.Notifier, params mcp.GetPromptRequestParams) (mcp.GetPromptResult, error) {
	if params.Arguments["text"] == "" {
		return mcp.GetPromptResult{}, errors.New("input text cannot be empty")
	}

	err := n.Notify(ctx, "test/notification", map[string]any{
		"message": "Processing text",
	})
	if err != nil {
		fmt.Printf("Failed to send notification: %v\n", err)
	}

	return mcp.GetPromptResult{
		Messages: []mcp.PromptMessage{
			{
				Role: mcp.RoleAssistant,
				Content: mcp.TextContent{
					Type: "text",
					Text: "Processed: " + params.Arguments["text"],
				},
			},
		},
	}, nil
}
