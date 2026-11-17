// A native fake adapter: no shell, printf escapes or Unix-only tools.
package main

import (
	"encoding/json"
	"io"
	"os"
	"strings"
	"time"
)

func main() {
	var req map[string]any
	if err := json.NewDecoder(os.Stdin).Decode(&req); err != nil {
		os.Exit(2)
	}
	switch os.Args[1] {
	case "describe":
		if started := os.Getenv("TEST_INTAKE_STARTED"); started != "" {
			if err := os.WriteFile(started, nil, 0600); err != nil {
				os.Exit(2)
			}
			time.Sleep(20 * time.Second)
		}
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"name": "native", "contractVersion": 1, "calls": []string{"describe", "list-untracked", "mark-tracked"}})
	case "list-untracked":
		switch req["for"] {
		case "invalid-utf8":
			_, _ = os.Stdout.Write([]byte{'{', '"', 'x', '"', ':', '"', 0xff, '"', '}'})
		case "astral-cap":
			_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"items": []map[string]string{{"externalId": strings.Repeat("😀", 512), "title": "t"}}})
		case "cap":
			_, _ = io.WriteString(os.Stdout, strings.Repeat("x", 2<<20))
		case "timeout":
			time.Sleep(10 * time.Second)
		case "refuse":
			_, _ = io.WriteString(os.Stderr, "refused\x1b[31m")
			os.Exit(3)
		default:
			_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"items": []map[string]string{{"externalId": "https://example.test/1", "title": "\x1b[2J😀"}}})
		}
	case "mark-tracked":
		if req["externalId"] == nil || req["task"] == nil {
			os.Exit(2)
		}
		_, _ = io.WriteString(os.Stdout, "{}")
	default:
		os.Exit(2)
	}
}
