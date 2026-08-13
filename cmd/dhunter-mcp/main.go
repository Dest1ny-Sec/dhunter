// Command dhunter-mcp is the Dhunter web-pentest toolbelt MCP server.
//
// It exposes the original Dhunter toolset (recon / info gathering /
// fingerprinting / active probing / AI-assisted PoC) as a streamable-HTTP
// MCP endpoint for LLM agents. Protocol: JSON-RPC 2.0 over HTTP at
// /message, gated by a bearer token.
package main

import (
	"flag"
	"log"
	"os"

	"github.com/dhunter/dhunter/internal/toolbelt"
)

func main() {
	var (
		addr   = flag.String("addr", "0.0.0.0:9124", "listen address")
		token  = flag.String("t", "", "bearer token (required)")
	)
	flag.Parse()

	if *token == "" {
		*token = os.Getenv("DHUNTER_MCP_TOKEN")
	}
	if *token == "" {
		log.Fatal("dhunter-mcp: a bearer token is required (-t or DHUNTER_MCP_TOKEN)")
	}
	if err := toolbelt.Serve(*addr, *token); err != nil {
		log.Fatalf("dhunter-mcp: %v", err)
	}
}
