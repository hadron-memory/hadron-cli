package chat

import (
	"encoding/json"
	"errors"
	"io/fs"
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
	Node        string
	Memory      string
	MessagesLoc string
	Identity    string
	Role        string
}

// projectConfigJSON is the on-disk shape (only the fields chat cares about).
// chat.node is a single node URN packing memory + messagesLoc (the message
// parent); memory/messagesLoc are the equivalent two-field form the push channel
// uses. Either identifies the chat.
type projectConfigJSON struct {
	Handle string `json:"handle"`
	Chat   struct {
		Node        string `json:"node"`
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
	pc, _ := findProjectChat()
	return pc
}

// configFile is how the ancestor search ended, for `chat config show` and
// `chat doctor` (#477). loadProjectChat's callers ignore it, which is exactly
// why a malformed or unreadable file used to be invisible.
type configFile struct {
	// Path is the file the search stopped at; "" when none was found.
	Path string
	// Status is "loaded", "malformed" (found, did not parse — the search stops
	// there and nothing from it is used) or "absent".
	Status string
	// Problem is the parse error of a malformed file. encoding/json's syntax
	// and type errors name an offset or a Go type, never the file's bytes, so
	// it cannot echo an appKey.
	Problem string
	// Skipped are nearer files that exist but could not be read; the search
	// walks past them to an ancestor.
	Skipped []skippedConfig
}

type skippedConfig struct {
	Path   string
	Reason string
}

// findProjectChat is loadProjectChat with its trace. The search rules are
// unchanged: the first PARSEABLE-or-not file ends the search, and a file that
// cannot be read at all is walked past.
func findProjectChat() (projectChat, configFile) {
	cf := configFile{Status: "absent"}
	dir, err := os.Getwd()
	if err != nil {
		return projectChat{}, cf
	}
	for {
		path := filepath.Join(dir, ".hadron", "config.json")
		raw, err := os.ReadFile(path)
		if err == nil {
			cf.Path = path
			var c projectConfigJSON
			if err := json.Unmarshal(raw, &c); err != nil {
				cf.Status, cf.Problem = "malformed", err.Error()
				return projectChat{}, cf
			}
			cf.Status = "loaded"
			return projectChat{
				Handle:      c.Handle,
				Node:        c.Chat.Node,
				Memory:      c.Chat.Memory,
				MessagesLoc: c.Chat.MessagesLoc,
				Identity:    c.Chat.Identity,
				Role:        c.Chat.Role,
			}, cf
		}
		if !errors.Is(err, fs.ErrNotExist) {
			cf.Skipped = append(cf.Skipped, skippedConfig{Path: path, Reason: err.Error()})
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return projectChat{}, cf // reached the filesystem root
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
