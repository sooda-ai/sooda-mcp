package mcpclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Client is a thin HTTP client for the Sooda platform API.
// It wraps the directory, relay, and session result endpoints.
type Client struct {
	BaseURL    string
	APIKey     string
	HTTPClient *http.Client
}

// New creates a Client with sensible defaults.
func New(baseURL, apiKey string) *Client {
	return &Client{
		BaseURL: baseURL,
		APIKey:  apiKey,
		HTTPClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// DirectoryEntry represents an agent returned by the directory endpoint.
type DirectoryEntry struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	AgentCard   json.RawMessage `json:"agent_card,omitempty"`
}

// Discover calls GET /api/v1/directory and returns connected agents.
func (c *Client) Discover(ctx context.Context) ([]DirectoryEntry, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", c.BaseURL+"/api/v1/directory", nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.APIKey)

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("directory returned HTTP %d: %s", resp.StatusCode, truncate(body, 200))
	}

	var entries []DirectoryEntry
	if err := json.NewDecoder(resp.Body).Decode(&entries); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return entries, nil
}

// RelayResponse is the result from the relay endpoint.
type RelayResponse struct {
	Status      string          `json:"status"`
	SessionID   string          `json:"session_id"`
	ContextID   string          `json:"context_id,omitempty"`
	A2ATaskID   string          `json:"a2a_task_id,omitempty"`
	A2AResponse json.RawMessage `json:"a2a_response,omitempty"`
	Message     string          `json:"message,omitempty"`
}

// Relay calls POST /api/v1/relay/{targetName} with an A2A message/send payload.
func (c *Client) Relay(ctx context.Context, targetName, message, contextID string) (*RelayResponse, error) {
	payload := map[string]any{
		"jsonrpc": "2.0",
		"id":      "1",
		"method":  "message/send",
		"params": map[string]any{
			"message": map[string]any{
				"role": "user",
				"parts": []map[string]any{
					{"type": "text", "text": message},
				},
			},
		},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal payload: %w", err)
	}

	url := fmt.Sprintf("%s/api/v1/relay/%s", c.BaseURL, targetName)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.APIKey)
	if contextID != "" {
		req.Header.Set("X-Sooda-Context-ID", contextID)
	}

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	// Parse JSON-RPC envelope
	var envelope struct {
		Result *RelayResponse `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(respBody, &envelope); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	if envelope.Error != nil {
		return nil, fmt.Errorf("relay error %d: %s", envelope.Error.Code, envelope.Error.Message)
	}
	if envelope.Result == nil {
		return nil, fmt.Errorf("relay returned empty result")
	}
	return envelope.Result, nil
}

// ResultResponse is the result from the session result polling endpoint.
type ResultResponse struct {
	SessionID          string          `json:"session_id"`
	Status             string          `json:"status"`
	ContextID          string          `json:"context_id,omitempty"`
	A2ATaskID          string          `json:"a2a_task_id,omitempty"`
	Response           json.RawMessage `json:"response,omitempty"`
	ResponseReceivedAt string          `json:"response_received_at,omitempty"`
}

// CheckResult calls GET /api/v1/sessions/{sessionID}/result.
func (c *Client) CheckResult(ctx context.Context, sessionID string) (*ResultResponse, error) {
	url := fmt.Sprintf("%s/api/v1/sessions/%s/result", c.BaseURL, sessionID)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.APIKey)

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("result returned HTTP %d: %s", resp.StatusCode, truncate(body, 200))
	}

	var result ResultResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return &result, nil
}

// BrowseEntry represents an agent in the public directory.
type BrowseEntry struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	AgentCard   json.RawMessage `json:"agent_card,omitempty"`
	Categories  []string        `json:"categories,omitempty"`
	AgentType   string          `json:"agent_type"`
	Connected   bool            `json:"connected"`
}

// DiscoverAll calls GET /api/v1/directory?scope=all and returns all discoverable agents.
func (c *Client) DiscoverAll(ctx context.Context, category string) ([]BrowseEntry, error) {
	url := c.BaseURL + "/api/v1/directory?scope=all"
	if category != "" {
		url += "&category=" + category
	}
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.APIKey)

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("browse returned HTTP %d: %s", resp.StatusCode, truncate(body, 200))
	}

	var entries []BrowseEntry
	if err := json.NewDecoder(resp.Body).Decode(&entries); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return entries, nil
}

// ConnectResponse is the result from the connect endpoint.
type ConnectResponse struct {
	Status  string `json:"status,omitempty"`
	Message string `json:"message,omitempty"`
	Error   string `json:"error,omitempty"`
}

// Connect calls POST /api/v1/connect/{agentName}.
func (c *Client) Connect(ctx context.Context, agentName, message string) (*ConnectResponse, error) {
	var body []byte
	if message != "" {
		body, _ = json.Marshal(map[string]string{"message": message})
	}

	url := fmt.Sprintf("%s/api/v1/connect/%s", c.BaseURL, agentName)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.APIKey)

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	var result ConnectResponse
	json.Unmarshal(respBody, &result)

	if resp.StatusCode >= 400 {
		if result.Error != "" {
			return nil, fmt.Errorf("%s", result.Error)
		}
		return nil, fmt.Errorf("connect returned HTTP %d: %s", resp.StatusCode, truncate(respBody, 200))
	}
	return &result, nil
}

// ConnRequestEntry represents a pending connection request.
type ConnRequestEntry struct {
	ID        string `json:"id"`
	FromName  string `json:"from_agent_name,omitempty"`
	ToName    string `json:"to_agent_name,omitempty"`
	Status    string `json:"status"`
	Message   string `json:"message,omitempty"`
	CreatedAt string `json:"created_at"`
}

// ListConnectionRequests calls GET /api/v1/connect/requests.
func (c *Client) ListConnectionRequests(ctx context.Context, direction string) ([]ConnRequestEntry, error) {
	url := c.BaseURL + "/api/v1/connect/requests"
	if direction != "" {
		url += "?direction=" + direction
	}
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.APIKey)

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("requests returned HTTP %d: %s", resp.StatusCode, truncate(body, 200))
	}

	var entries []ConnRequestEntry
	if err := json.NewDecoder(resp.Body).Decode(&entries); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return entries, nil
}

// AcceptConnection calls POST /api/v1/connect/{agentName}/accept.
func (c *Client) AcceptConnection(ctx context.Context, agentName string) error {
	url := fmt.Sprintf("%s/api/v1/connect/%s/accept", c.BaseURL, agentName)
	req, err := http.NewRequestWithContext(ctx, "POST", url, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.APIKey)

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("accept returned HTTP %d: %s", resp.StatusCode, truncate(body, 200))
	}
	return nil
}

// RejectConnection calls POST /api/v1/connect/{agentName}/reject.
func (c *Client) RejectConnection(ctx context.Context, agentName string) error {
	url := fmt.Sprintf("%s/api/v1/connect/%s/reject", c.BaseURL, agentName)
	req, err := http.NewRequestWithContext(ctx, "POST", url, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.APIKey)

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("reject returned HTTP %d: %s", resp.StatusCode, truncate(body, 200))
	}
	return nil
}

// InboxItem represents a message in the agent's inbox.
type InboxItem struct {
	MessageID       string `json:"message_id"`
	SessionID       string `json:"session_id"`
	ContextID       string `json:"context_id,omitempty"`
	From            string `json:"from"`
	FromDescription string `json:"from_description,omitempty"`
	Text            string `json:"text"`
	CreatedAt       string `json:"created_at"`
}

// CheckInbox calls GET /api/v1/inbox.
func (c *Client) CheckInbox(ctx context.Context, since string) ([]InboxItem, error) {
	url := c.BaseURL + "/api/v1/inbox"
	if since != "" {
		url += "?since=" + since
	}
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.APIKey)

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("inbox returned HTTP %d: %s", resp.StatusCode, truncate(body, 200))
	}

	var items []InboxItem
	if err := json.NewDecoder(resp.Body).Decode(&items); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return items, nil
}

// ConversationSummary represents a conversation from the conversations endpoint.
type ConversationSummary struct {
	ContextID    string   `json:"context_id"`
	Agents       []string `json:"agents"`
	Turns        int      `json:"turns"`
	Status       string   `json:"status"`
	LastMessage  string   `json:"last_message"`
	StartedAt    string   `json:"started_at"`
	LastActivity string   `json:"last_activity"`
}

// ListConversations calls GET /api/v1/conversations.
func (c *Client) ListConversations(ctx context.Context, limit int) ([]ConversationSummary, error) {
	url := fmt.Sprintf("%s/api/v1/conversations?limit=%d", c.BaseURL, limit)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.APIKey)

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("conversations returned HTTP %d: %s", resp.StatusCode, truncate(body, 200))
	}

	var items []ConversationSummary
	if err := json.NewDecoder(resp.Body).Decode(&items); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return items, nil
}

func truncate(b []byte, maxLen int) string {
	if len(b) <= maxLen {
		return string(b)
	}
	return string(b[:maxLen]) + "..."
}
