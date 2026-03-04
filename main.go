package main

import (
	"fmt"
	"os"
)

// version is set via ldflags at build time.
var version = "dev"

func main() {
	if len(os.Args) < 2 {
		runServe()
		return
	}

	switch os.Args[1] {
	case "setup":
		runSetup()
	case "version":
		fmt.Printf("sooda-mcp %s\n", version)
	case "help", "--help", "-h":
		fmt.Println("Usage: sooda-mcp [command]")
		fmt.Println()
		fmt.Println("Commands:")
		fmt.Println("  (none)    Run MCP stdio server (default)")
		fmt.Println("  setup     Interactive setup: sign up + configure Claude Desktop")
		fmt.Println("  version   Print version")
		fmt.Println("  help      Show this help")
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\nRun 'sooda-mcp help' for usage.\n", os.Args[1])
		os.Exit(1)
	}
}
