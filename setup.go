package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const defaultSoodaURL = "https://sooda.ai"

func runSetup() {
	fmt.Println("=== Sooda MCP Setup ===")
	fmt.Println()
	fmt.Println("This will:")
	fmt.Println("  1. Create a free Sooda agent account")
	fmt.Println("  2. Configure Claude Desktop to use Sooda")
	fmt.Println()

	scanner := bufio.NewScanner(os.Stdin)

	name := prompt(scanner, "Agent name (lowercase, letters/numbers/hyphens): ")
	if name == "" {
		fatalf("Agent name is required.")
	}

	email := prompt(scanner, "Email: ")
	if email == "" {
		fatalf("Email is required.")
	}

	soodaURL := os.Getenv("SOODA_URL")
	if soodaURL == "" {
		soodaURL = defaultSoodaURL
	}

	fmt.Println()
	fmt.Printf("Signing up as %q on %s...\n", name, soodaURL)

	apiKey, connectedAgents, err := signup(soodaURL, name, email)
	if err != nil {
		fatalf("Signup failed: %s", err)
	}

	fmt.Printf("Account created. API key: %s...%s\n", apiKey[:6], apiKey[len(apiKey)-4:])
	if len(connectedAgents) > 0 {
		fmt.Printf("Connected to %d agents: %s\n", len(connectedAgents), strings.Join(connectedAgents, ", "))
	}

	// Find and update Claude Desktop config
	configPath := claudeDesktopConfigPath()
	fmt.Println()
	fmt.Printf("Claude Desktop config: %s\n", configPath)

	execPath, err := os.Executable()
	if err != nil {
		fatalf("Could not determine binary path: %s", err)
	}
	// Resolve any symlinks to get the real path
	execPath, err = filepath.EvalSymlinks(execPath)
	if err != nil {
		fatalf("Could not resolve binary path: %s", err)
	}

	if err := writeClaudeConfig(configPath, execPath, apiKey, scanner); err != nil {
		fatalf("Failed to write config: %s", err)
	}

	fmt.Println()
	fmt.Println("Setup complete!")
	fmt.Println()
	fmt.Println("Next steps:")
	fmt.Println("  1. Restart Claude Desktop")
	fmt.Println("  2. Ask Claude: \"Use sooda_discover to see what agents are available\"")
	fmt.Println("  3. Try: \"Ask travelwise to plan a trip to Tokyo\"")
}

func prompt(scanner *bufio.Scanner, label string) string {
	fmt.Print(label)
	if !scanner.Scan() {
		return ""
	}
	return strings.TrimSpace(scanner.Text())
}

type signupRequest struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

type signupResponse struct {
	AgentID         string   `json:"agent_id"`
	AgentName       string   `json:"agent_name"`
	APIKey          string   `json:"api_key"`
	ConnectedAgents []string `json:"connected_agents"`
}

func signup(baseURL, name, email string) (apiKey string, connectedAgents []string, err error) {
	body, err := json.Marshal(signupRequest{Name: name, Email: email})
	if err != nil {
		return "", nil, fmt.Errorf("marshal request: %w", err)
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Post(baseURL+"/api/v1/signup", "application/json", bytes.NewReader(body))
	if err != nil {
		return "", nil, fmt.Errorf("network error: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode == http.StatusConflict {
		return "", nil, fmt.Errorf("agent name %q is already taken — try a different name", name)
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		return "", nil, fmt.Errorf("rate limited — too many signups from this IP, try again later")
	}
	if resp.StatusCode != http.StatusCreated {
		return "", nil, fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(respBody))
	}

	var result signupResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", nil, fmt.Errorf("parse response: %w", err)
	}

	return result.APIKey, result.ConnectedAgents, nil
}

func claudeDesktopConfigPath() string {
	switch runtime.GOOS {
	case "darwin":
		home, _ := os.UserHomeDir()
		return filepath.Join(home, "Library", "Application Support", "Claude", "claude_desktop_config.json")
	case "windows":
		appdata := os.Getenv("APPDATA")
		if appdata == "" {
			home, _ := os.UserHomeDir()
			appdata = filepath.Join(home, "AppData", "Roaming")
		}
		return filepath.Join(appdata, "Claude", "claude_desktop_config.json")
	default: // linux
		home, _ := os.UserHomeDir()
		return filepath.Join(home, ".config", "Claude", "claude_desktop_config.json")
	}
}

// claudeDesktopConfig represents the Claude Desktop configuration file structure.
type claudeDesktopConfig struct {
	MCPServers map[string]json.RawMessage `json:"mcpServers"`
	Rest       map[string]json.RawMessage `json:"-"`
}

func writeClaudeConfig(configPath, execPath, apiKey string, scanner *bufio.Scanner) error {
	// Build the sooda MCP server entry
	soodaEntry := map[string]any{
		"command": execPath,
		"env": map[string]string{
			"SOODA_API_KEY": apiKey,
		},
	}

	// Read existing config or start fresh
	var rawConfig map[string]json.RawMessage

	data, err := os.ReadFile(configPath)
	if err == nil {
		// File exists — parse it
		if err := json.Unmarshal(data, &rawConfig); err != nil {
			return fmt.Errorf("existing config is malformed JSON: %w\nManual fix: edit %s", err, configPath)
		}

		// Check for existing sooda entry
		if servers, ok := rawConfig["mcpServers"]; ok {
			var mcpServers map[string]json.RawMessage
			if err := json.Unmarshal(servers, &mcpServers); err == nil {
				if _, exists := mcpServers["sooda"]; exists {
					answer := prompt(scanner, "Sooda is already configured in Claude Desktop. Overwrite? [y/N]: ")
					if !strings.HasPrefix(strings.ToLower(answer), "y") {
						fmt.Println("Keeping existing config. You can manually update", configPath)
						return nil
					}
				}
			}
		}

		// Backup existing config
		backupPath := configPath + ".bak"
		if err := os.WriteFile(backupPath, data, 0600); err != nil {
			return fmt.Errorf("failed to create backup at %s: %w", backupPath, err)
		}
		fmt.Printf("Backed up existing config to %s\n", backupPath)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("cannot read %s: %w", configPath, err)
	} else {
		// File doesn't exist — start with empty config
		rawConfig = make(map[string]json.RawMessage)
	}

	// Merge sooda into mcpServers
	var mcpServers map[string]json.RawMessage
	if servers, ok := rawConfig["mcpServers"]; ok {
		if err := json.Unmarshal(servers, &mcpServers); err != nil {
			mcpServers = make(map[string]json.RawMessage)
		}
	} else {
		mcpServers = make(map[string]json.RawMessage)
	}

	soodaBytes, _ := json.Marshal(soodaEntry)
	mcpServers["sooda"] = json.RawMessage(soodaBytes)

	serversBytes, _ := json.Marshal(mcpServers)
	rawConfig["mcpServers"] = json.RawMessage(serversBytes)

	// Write config with indentation
	output, err := json.MarshalIndent(rawConfig, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}

	// Ensure parent directory exists
	if err := os.MkdirAll(filepath.Dir(configPath), 0755); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}

	if err := os.WriteFile(configPath, output, 0600); err != nil {
		return fmt.Errorf("write config: %w", err)
	}

	fmt.Printf("Wrote Claude Desktop config to %s\n", configPath)
	return nil
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "Error: "+format+"\n", args...)
	os.Exit(1)
}
