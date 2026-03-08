package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"

	"github.com/Ask149/aidaemon/internal/auth"
	"github.com/Ask149/aidaemon/internal/provider"
	"github.com/Ask149/aidaemon/internal/provider/copilot"
)

func main() {
	tm, err := auth.NewTokenManager()
	if err != nil {
		log.Fatal(err)
	}
	if _, err := tm.GetToken(); err != nil {
		log.Fatal(err)
	}

	p := copilot.New(tm)

	req := provider.ChatRequest{
		Model: "claude-sonnet-4.5",
		Messages: []provider.Message{
			{Role: "system", Content: "You are a helpful assistant. Use tools when needed."},
			{Role: "user", Content: "List the files on my desktop using the run_command tool"},
		},
		Tools: []provider.ToolDef{
			{
				Type: "function",
				Function: provider.FuncDef{
					Name:        "run_command",
					Description: "Execute a shell command",
					Parameters: map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"command": map[string]interface{}{
								"type":        "string",
								"description": "The command to run",
							},
						},
						"required": []string{"command"},
					},
				},
			},
		},
	}

	resp, err := p.Chat(context.Background(), req)
	if err != nil {
		log.Fatal(err)
	}

	out, _ := json.MarshalIndent(resp, "", "  ")
	fmt.Println(string(out))
	os.Exit(0)
}
