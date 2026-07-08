package chat

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// projectChat is the team-chat slice of the project-local .hadron/config.json —
// the SAME file the hadron-client push channel reads, so an agent configures its
// identity and chat coordinates once and both surfaces use it. handle lives at
// the top level; memory/messagesLoc under `chat` (channel-compatible). identity
// and role are CLI-only additions the channel ignores.
type projectChat struct {
	Handle      string
	Memory      string
	MessagesLoc string
	Identity    string
	Role        string
}

// projectConfigJSON is the on-disk shape (only the fields chat cares about).
type projectConfigJSON struct {
	Handle string `json:"handle"`
	Chat   struct {
		Memory      string `json:"memory"`
		MessagesLoc string `json:"messagesLoc"`
		Identity    string `json:"identity"`
		Role        string `json:"role"`
	} `json:"chat"`
}

// loadProjectChat reads .hadron/config.json, searching the working directory and
// its ancestors (so the command works from a subdirectory). A missing or
// unreadable file yields a zero projectChat, never an error — config is a
// convenience layered under flags/env, not a requirement.
func loadProjectChat() projectChat {
	dir, err := os.Getwd()
	if err != nil {
		return projectChat{}
	}
	for {
		path := filepath.Join(dir, ".hadron", "config.json")
		if raw, err := os.ReadFile(path); err == nil {
			var c projectConfigJSON
			if json.Unmarshal(raw, &c) == nil {
				return projectChat{
					Handle:      c.Handle,
					Memory:      c.Chat.Memory,
					MessagesLoc: c.Chat.MessagesLoc,
					Identity:    c.Chat.Identity,
					Role:        c.Chat.Role,
				}
			}
			return projectChat{}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return projectChat{} // reached the filesystem root
		}
		dir = parent
	}
}

// firstNonEmpty returns the first non-blank value — the flag/env/config
// precedence helper.
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
