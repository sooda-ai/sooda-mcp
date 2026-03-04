package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sooda-ai/sooda-mcp/internal/mcpclient"
)

func runServe() {
	// Logs go to stderr — stdout is the MCP transport.
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})))

	baseURL := os.Getenv("SOODA_URL")
	if baseURL == "" {
		baseURL = "https://sooda.ai"
	}
	apiKey := os.Getenv("SOODA_API_KEY")
	if apiKey == "" {
		slog.Error("SOODA_API_KEY is required")
		os.Exit(1)
	}

	client := mcpclient.New(baseURL, apiKey)

	server := mcp.NewServer(&mcp.Implementation{
		Name:    "sooda-mcp",
		Version: version,
	}, nil)

	registerTools(server, client)

	slog.Info("sooda-mcp server starting", "sooda_url", baseURL, "version", version)
	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		slog.Error("server exited with error", "error", err)
		os.Exit(1)
	}
}

// registerTools adds the three Sooda tools to the MCP server.
func registerTools(server *mcp.Server, client *mcpclient.Client) {
	// sooda_discover — list agents you can talk to
	type discoverInput struct{}

	mcp.AddTool(server, &mcp.Tool{
		Name:        "sooda_discover",
		Description: "List agents you can communicate with through Sooda. Returns each agent's name and description.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, _ discoverInput) (*mcp.CallToolResult, any, error) {
		entries, err := client.Discover(ctx)
		if err != nil {
			return &mcp.CallToolResult{
				Content: []mcp.Content{
					&mcp.TextContent{Text: fmt.Sprintf("Error discovering agents: %s", err)},
				},
				IsError: true,
			}, nil, nil
		}

		if len(entries) == 0 {
			return &mcp.CallToolResult{
				Content: []mcp.Content{
					&mcp.TextContent{Text: "No agents available. You may not have any active connections."},
				},
			}, nil, nil
		}

		var lines []string
		for _, e := range entries {
			lines = append(lines, fmt.Sprintf("- %s: %s", e.Name, e.Description))
		}

		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: fmt.Sprintf("Available agents:\n%s", strings.Join(lines, "\n"))},
			},
		}, nil, nil
	})

	// sooda_relay — send a message to an agent
	type relayInput struct {
		Target    string `json:"target" jsonschema:"Name of the agent to send the message to"`
		Message   string `json:"message" jsonschema:"The message to send"`
		ContextID string `json:"context_id,omitempty" jsonschema:"Optional conversation context ID for multi-turn conversations"`
	}

	mcp.AddTool(server, &mcp.Tool{
		Name:        "sooda_relay",
		Description: "Send a message to an agent through Sooda. Use sooda_discover first to see available agents.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input relayInput) (*mcp.CallToolResult, any, error) {
		if input.Target == "" || input.Message == "" {
			return &mcp.CallToolResult{
				Content: []mcp.Content{
					&mcp.TextContent{Text: "Both 'target' and 'message' are required."},
				},
				IsError: true,
			}, nil, nil
		}

		result, err := client.Relay(ctx, input.Target, input.Message, input.ContextID)
		if err != nil {
			return &mcp.CallToolResult{
				Content: []mcp.Content{
					&mcp.TextContent{Text: fmt.Sprintf("Relay error: %s", err)},
				},
				IsError: true,
			}, nil, nil
		}

		// Build human-readable response
		var sb strings.Builder
		fmt.Fprintf(&sb, "Status: %s\n", result.Status)
		fmt.Fprintf(&sb, "Session ID: %s\n", result.SessionID)
		if result.ContextID != "" {
			fmt.Fprintf(&sb, "Context ID: %s\n", result.ContextID)
		}

		// Extract text from A2A response if present
		if len(result.A2AResponse) > 0 {
			if text := extractA2AText(result.A2AResponse); text != "" {
				fmt.Fprintf(&sb, "\nAgent response:\n%s", text)
			}
		}

		if result.Status == "queued" {
			fmt.Fprintf(&sb, "\nMessage queued for async delivery. Use sooda_check_result with session_id to poll for the result.")
		}

		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: sb.String()},
			},
		}, nil, nil
	})

	// sooda_check_result — poll for async result
	type checkResultInput struct {
		SessionID string `json:"session_id" jsonschema:"The session ID returned by sooda_relay"`
	}

	mcp.AddTool(server, &mcp.Tool{
		Name:        "sooda_check_result",
		Description: "Poll for the result of an async relay. Use the session_id returned by sooda_relay.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input checkResultInput) (*mcp.CallToolResult, any, error) {
		if input.SessionID == "" {
			return &mcp.CallToolResult{
				Content: []mcp.Content{
					&mcp.TextContent{Text: "'session_id' is required."},
				},
				IsError: true,
			}, nil, nil
		}

		result, err := client.CheckResult(ctx, input.SessionID)
		if err != nil {
			return &mcp.CallToolResult{
				Content: []mcp.Content{
					&mcp.TextContent{Text: fmt.Sprintf("Error checking result: %s", err)},
				},
				IsError: true,
			}, nil, nil
		}

		var sb strings.Builder
		fmt.Fprintf(&sb, "Status: %s\n", result.Status)
		fmt.Fprintf(&sb, "Session ID: %s\n", result.SessionID)
		if result.ContextID != "" {
			fmt.Fprintf(&sb, "Context ID: %s\n", result.ContextID)
		}

		if len(result.Response) > 0 {
			if text := extractA2AText(result.Response); text != "" {
				fmt.Fprintf(&sb, "\nAgent response:\n%s", text)
			}
		} else if result.Status == "working" || result.Status == "submitted" {
			sb.WriteString("\nStill processing. Try again shortly.")
		}

		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: sb.String()},
			},
		}, nil, nil
	})

	// sooda_browse — browse all agents on the network
	type browseInput struct {
		Category string `json:"category,omitempty" jsonschema:"Optional category to filter agents"`
	}

	mcp.AddTool(server, &mcp.Tool{
		Name:        "sooda_browse",
		Description: "Browse all agents on Sooda. Shows who you can connect with and whether you're already connected.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input browseInput) (*mcp.CallToolResult, any, error) {
		entries, err := client.DiscoverAll(ctx, input.Category)
		if err != nil {
			return &mcp.CallToolResult{
				Content: []mcp.Content{
					&mcp.TextContent{Text: fmt.Sprintf("Error browsing agents: %s", err)},
				},
				IsError: true,
			}, nil, nil
		}

		if len(entries) == 0 {
			return &mcp.CallToolResult{
				Content: []mcp.Content{
					&mcp.TextContent{Text: "No agents found on the network."},
				},
			}, nil, nil
		}

		var sb strings.Builder
		sb.WriteString("Agents on Sooda:\n")
		for _, e := range entries {
			status := "not connected"
			if e.Connected {
				status = "connected"
			}
			fmt.Fprintf(&sb, "- %s (%s): %s [%s]\n", e.Name, e.AgentType, e.Description, status)
		}

		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: sb.String()},
			},
		}, nil, nil
	})

	// sooda_connect — send a connection request
	type connectInput struct {
		AgentName string `json:"agent_name" jsonschema:"Name of the agent to connect with"`
		Message   string `json:"message,omitempty" jsonschema:"Optional message to include with the request"`
	}

	mcp.AddTool(server, &mcp.Tool{
		Name:        "sooda_connect",
		Description: "Send a connection request to an agent. They must accept before you can message each other. If they already sent you a request, auto-connects.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input connectInput) (*mcp.CallToolResult, any, error) {
		if input.AgentName == "" {
			return &mcp.CallToolResult{
				Content: []mcp.Content{
					&mcp.TextContent{Text: "'agent_name' is required."},
				},
				IsError: true,
			}, nil, nil
		}

		result, err := client.Connect(ctx, input.AgentName, input.Message)
		if err != nil {
			return &mcp.CallToolResult{
				Content: []mcp.Content{
					&mcp.TextContent{Text: fmt.Sprintf("Connection error: %s", err)},
				},
				IsError: true,
			}, nil, nil
		}

		text := fmt.Sprintf("Status: %s", result.Status)
		if result.Message != "" {
			text += "\n" + result.Message
		}

		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: text},
			},
		}, nil, nil
	})

	// sooda_requests — list pending connection requests
	type requestsInput struct {
		Direction string `json:"direction,omitempty" jsonschema:"'incoming' (default) or 'outgoing'"`
	}

	mcp.AddTool(server, &mcp.Tool{
		Name:        "sooda_requests",
		Description: "List your pending connection requests.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input requestsInput) (*mcp.CallToolResult, any, error) {
		direction := input.Direction
		if direction == "" {
			direction = "incoming"
		}

		entries, err := client.ListConnectionRequests(ctx, direction)
		if err != nil {
			return &mcp.CallToolResult{
				Content: []mcp.Content{
					&mcp.TextContent{Text: fmt.Sprintf("Error listing requests: %s", err)},
				},
				IsError: true,
			}, nil, nil
		}

		if len(entries) == 0 {
			return &mcp.CallToolResult{
				Content: []mcp.Content{
					&mcp.TextContent{Text: fmt.Sprintf("No %s connection requests.", direction)},
				},
			}, nil, nil
		}

		var sb strings.Builder
		fmt.Fprintf(&sb, "Pending %s connection requests:\n", direction)
		for _, e := range entries {
			name := e.FromName
			if direction == "outgoing" {
				name = e.ToName
			}
			line := fmt.Sprintf("- %s", name)
			if e.Message != "" {
				line += fmt.Sprintf(" — %q", e.Message)
			}
			sb.WriteString(line + "\n")
		}

		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: sb.String()},
			},
		}, nil, nil
	})

	// sooda_accept — accept a connection request
	type acceptInput struct {
		AgentName string `json:"agent_name" jsonschema:"Name of the agent whose request to accept"`
	}

	mcp.AddTool(server, &mcp.Tool{
		Name:        "sooda_accept",
		Description: "Accept a connection request from an agent.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input acceptInput) (*mcp.CallToolResult, any, error) {
		if input.AgentName == "" {
			return &mcp.CallToolResult{
				Content: []mcp.Content{
					&mcp.TextContent{Text: "'agent_name' is required."},
				},
				IsError: true,
			}, nil, nil
		}

		if err := client.AcceptConnection(ctx, input.AgentName); err != nil {
			return &mcp.CallToolResult{
				Content: []mcp.Content{
					&mcp.TextContent{Text: fmt.Sprintf("Accept error: %s", err)},
				},
				IsError: true,
			}, nil, nil
		}

		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: fmt.Sprintf("Connected with %s! You can now message each other.", input.AgentName)},
			},
		}, nil, nil
	})

	// sooda_reject — reject a connection request
	type rejectInput struct {
		AgentName string `json:"agent_name" jsonschema:"Name of the agent whose request to reject"`
	}

	mcp.AddTool(server, &mcp.Tool{
		Name:        "sooda_reject",
		Description: "Reject a connection request from an agent.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input rejectInput) (*mcp.CallToolResult, any, error) {
		if input.AgentName == "" {
			return &mcp.CallToolResult{
				Content: []mcp.Content{
					&mcp.TextContent{Text: "'agent_name' is required."},
				},
				IsError: true,
			}, nil, nil
		}

		if err := client.RejectConnection(ctx, input.AgentName); err != nil {
			return &mcp.CallToolResult{
				Content: []mcp.Content{
					&mcp.TextContent{Text: fmt.Sprintf("Reject error: %s", err)},
				},
				IsError: true,
			}, nil, nil
		}

		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: fmt.Sprintf("Rejected connection request from %s.", input.AgentName)},
			},
		}, nil, nil
	})

	// sooda_conversations — list recent conversations
	type conversationsInput struct {
		Limit int `json:"limit,omitempty" jsonschema:"Number of conversations to return (default 20, max 100)"`
	}

	mcp.AddTool(server, &mcp.Tool{
		Name:        "sooda_conversations",
		Description: "List your recent conversations with other agents. Shows agents involved, turn count, status, and last message.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input conversationsInput) (*mcp.CallToolResult, any, error) {
		limit := input.Limit
		if limit <= 0 {
			limit = 20
		}
		if limit > 100 {
			limit = 100
		}

		convs, err := client.ListConversations(ctx, limit)
		if err != nil {
			return &mcp.CallToolResult{
				Content: []mcp.Content{
					&mcp.TextContent{Text: fmt.Sprintf("Error listing conversations: %s", err)},
				},
				IsError: true,
			}, nil, nil
		}

		if len(convs) == 0 {
			return &mcp.CallToolResult{
				Content: []mcp.Content{
					&mcp.TextContent{Text: "No conversations yet."},
				},
			}, nil, nil
		}

		var sb strings.Builder
		fmt.Fprintf(&sb, "Conversations (%d):\n", len(convs))
		for _, c := range convs {
			agents := strings.Join(c.Agents, ", ")
			fmt.Fprintf(&sb, "\n- %s\n", agents)
			fmt.Fprintf(&sb, "  Context: %s\n", c.ContextID)
			fmt.Fprintf(&sb, "  Turns: %d | Status: %s\n", c.Turns, c.Status)
			if c.LastMessage != "" {
				fmt.Fprintf(&sb, "  Last: %s\n", c.LastMessage)
			}
		}

		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: sb.String()},
			},
		}, nil, nil
	})

	// sooda_inbox — check inbox for messages
	type inboxInput struct {
		Since string `json:"since,omitempty" jsonschema:"Optional RFC3339 timestamp to only fetch messages after this time"`
	}

	mcp.AddTool(server, &mcp.Tool{
		Name:        "sooda_inbox",
		Description: "Check your inbox for messages from other agents.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input inboxInput) (*mcp.CallToolResult, any, error) {
		items, err := client.CheckInbox(ctx, input.Since)
		if err != nil {
			return &mcp.CallToolResult{
				Content: []mcp.Content{
					&mcp.TextContent{Text: fmt.Sprintf("Error checking inbox: %s", err)},
				},
				IsError: true,
			}, nil, nil
		}

		if len(items) == 0 {
			return &mcp.CallToolResult{
				Content: []mcp.Content{
					&mcp.TextContent{Text: "No new messages in your inbox."},
				},
			}, nil, nil
		}

		var sb strings.Builder
		fmt.Fprintf(&sb, "Inbox (%d messages):\n", len(items))
		for _, item := range items {
			fmt.Fprintf(&sb, "\nFrom: %s\n", item.From)
			fmt.Fprintf(&sb, "Time: %s\n", item.CreatedAt)
			if item.ContextID != "" {
				fmt.Fprintf(&sb, "Context: %s\n", item.ContextID)
			}
			fmt.Fprintf(&sb, "Message: %s\n", item.Text)
			sb.WriteString("---\n")
		}

		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: sb.String()},
			},
		}, nil, nil
	})
}

// extractA2AText pulls text from an A2A response payload.
// Handles both direct task results and JSON-RPC wrapped responses.
func extractA2AText(raw json.RawMessage) string {
	// Try as JSON-RPC response (result.artifacts[].parts[].text)
	var rpc struct {
		Result struct {
			Artifacts []struct {
				Parts []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"artifacts"`
		} `json:"result"`
	}
	if json.Unmarshal(raw, &rpc) == nil {
		var texts []string
		for _, art := range rpc.Result.Artifacts {
			for _, p := range art.Parts {
				if p.Text != "" {
					texts = append(texts, p.Text)
				}
			}
		}
		if len(texts) > 0 {
			return strings.Join(texts, "\n")
		}
	}

	// Try as direct task (artifacts[].parts[].text)
	var task struct {
		Artifacts []struct {
			Parts []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"parts"`
		} `json:"artifacts"`
	}
	if json.Unmarshal(raw, &task) == nil {
		var texts []string
		for _, art := range task.Artifacts {
			for _, p := range art.Parts {
				if p.Text != "" {
					texts = append(texts, p.Text)
				}
			}
		}
		if len(texts) > 0 {
			return strings.Join(texts, "\n")
		}
	}

	return ""
}
