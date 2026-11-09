package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// #477 writer cut: `chat config set` edits ONLY the named chat.* keys of the
// nearest .hadron/config.json, keeping every other byte of every other key.

const setNode = "hrn:node:acme.com:tc:chats:api:messages"

// setCfg is deliberately awkward: key order, an exotic number, an escaped
// string, nested unknown keys and a secret must all survive untouched.
const setCfg = `{
  "zeta": 1.0e0,
  "appKey": "` + chatConfigSecret + `",
  "handle": "iris",
  "unrelated": {"token": "raw-\u00e9-unrelated", "n": [1, 2.50, null]},
  "chat": {"identity": "Claude", "pollIntervalMs": 10000, "messagesLoc": "old:loc"}
}`

func cfgPath(dir string) string { return filepath.Join(dir, ".hadron", "config.json") }

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func wantUsage(t *testing.T, err error) {
	t.Helper()
	if err == nil || exitCodeFor(err) != exitcode.Usage {
		t.Fatalf("err = %v, want a usage error", err)
	}
}

func TestChatConfigSetNodePreservesEverythingElse(t *testing.T) {
	dir := chatConfigDir(t, setCfg)
	out, err := runChat(t, "chat", "config", "set", "--node", setNode, "--json", "--server", "http://127.0.0.1:1")
	if err != nil {
		t.Fatalf("set: %v", err)
	}
	if strings.Contains(out, chatConfigSecret) {
		t.Errorf("output leaked the appKey: %s", out)
	}
	var dto struct {
		Path    string  `json:"path"`
		Created bool    `json:"created"`
		Backup  *string `json:"backup"`
		Set     []struct{ Key, Value string }
	}
	if err := json.Unmarshal([]byte(out), &dto); err != nil {
		t.Fatalf("json: %v (%s)", err, out)
	}
	if dto.Path != cfgPath(dir) || dto.Created || dto.Backup == nil || len(dto.Set) != 1 || dto.Set[0].Key != "node" {
		t.Errorf("dto = %+v", dto)
	}
	got := readFile(t, cfgPath(dir))
	// Every untouched raw value survives byte for byte.
	for _, frag := range []string{`1.0e0`, `"` + chatConfigSecret + `"`, `"raw-\u00e9-unrelated"`, `2.50`, `"pollIntervalMs": 10000`, `"messagesLoc": "old:loc"`, `"identity": "Claude"`} {
		if !strings.Contains(got, frag) {
			t.Errorf("lost %q:\n%s", frag, got)
		}
	}
	// Order kept: zeta, appKey, handle, unrelated, chat; node appended in chat.
	last := -1
	for _, k := range []string{`"zeta"`, `"appKey"`, `"handle"`, `"unrelated"`, `"chat"`, `"identity"`, `"pollIntervalMs"`, `"messagesLoc"`, `"node"`} {
		i := strings.Index(got, k)
		if i <= last {
			t.Errorf("key %s out of order (at %d, after %d):\n%s", k, i, last, got)
		}
		last = i
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(got), &parsed); err != nil {
		t.Fatalf("result is not valid JSON: %v", err)
	}
	if parsed["chat"].(map[string]any)["node"] != setNode {
		t.Errorf("chat.node = %v", parsed["chat"])
	}
	// The backup is the original, byte for byte.
	if b := readFile(t, *dto.Backup); b != setCfg {
		t.Errorf("backup differs from the original:\n%s", b)
	}
	// And the readers resolve what was written.
	sh, err := runChat(t, "chat", "config", "show", "--json")
	if err != nil || !strings.Contains(sh, `"form": "node"`) && !strings.Contains(sh, `"form":"node"`) {
		t.Errorf("show after set: %v %s", err, sh)
	}
}

func TestChatConfigSetTwoFieldFormTouchesOnlyNamedKeys(t *testing.T) {
	dir := chatConfigDir(t, setCfg)
	if _, err := runChat(t, "chat", "config", "set", "-m", "hrn:mem:acme.com:tc"); err != nil {
		t.Fatal(err)
	}
	var c struct {
		Chat map[string]any `json:"chat"`
	}
	if err := json.Unmarshal([]byte(readFile(t, cfgPath(dir))), &c); err != nil {
		t.Fatal(err)
	}
	if c.Chat["memory"] != "hrn:mem:acme.com:tc" || c.Chat["messagesLoc"] != "old:loc" || c.Chat["identity"] != "Claude" {
		t.Errorf("chat = %v, want memory set and messagesLoc/identity untouched", c.Chat)
	}
	if _, has := c.Chat["node"]; has {
		t.Errorf("node must not appear when only -m was given: %v", c.Chat)
	}
}

func TestChatConfigSetTrimsTrailingColonOnMessagesLoc(t *testing.T) {
	dir := chatConfigDir(t, `{}`)
	if _, err := runChat(t, "chat", "config", "set", "--messages-loc", "chats:api:messages:"); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, cfgPath(dir)); !strings.Contains(got, `"messagesLoc": "chats:api:messages"`) {
		t.Errorf("got %s", got)
	}
}

func TestChatConfigSetEditsNearestAncestorFile(t *testing.T) {
	dir := chatConfigDir(t, `{"appKey":"k"}`)
	// cwd is dir/a/b; the file lives at dir/.hadron — it must be the one edited
	// and no new file may appear beside the cwd.
	if _, err := runChat(t, "chat", "config", "set", "--node", setNode); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(readFile(t, cfgPath(dir)), setNode) {
		t.Error("ancestor config not updated")
	}
	if _, err := os.Stat(filepath.Join(dir, "a", "b", ".hadron")); err == nil {
		t.Error("created a second config in the working directory")
	}
}

func TestChatConfigSetCreatesWhenAbsentWith0600(t *testing.T) {
	dir := chatConfigDir(t, "")
	out, err := runChat(t, "chat", "config", "set", "--node", setNode, "--json")
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "a", "b", ".hadron", "config.json")
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatalf("not created: %v", err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600", fi.Mode().Perm())
	}
	if !strings.Contains(out, `"created": true`) && !strings.Contains(out, `"created":true`) {
		t.Errorf("created not reported: %s", out)
	}
	if _, err := os.Stat(p + ".bak"); err == nil {
		t.Error("a backup of a file that did not exist")
	}
}

func TestChatConfigSetKeepsFileMode(t *testing.T) {
	dir := chatConfigDir(t, `{"a":1}`)
	if err := os.Chmod(cfgPath(dir), 0o640); err != nil {
		t.Fatal(err)
	}
	if _, err := runChat(t, "chat", "config", "set", "--node", setNode); err != nil {
		t.Fatal(err)
	}
	fi, _ := os.Stat(cfgPath(dir))
	if fi.Mode().Perm() != 0o640 {
		t.Errorf("mode = %v, want 0640 kept", fi.Mode().Perm())
	}
}

func TestChatConfigSetRefusesWithoutWriting(t *testing.T) {
	cases := map[string]struct {
		cfg  string
		args []string
	}{
		"malformed JSON":       {`{"appKey":"k",`, []string{"--node", setNode}},
		"top level an array":   {`[1,2]`, []string{"--node", setNode}},
		"chat not an object":   {`{"chat":"oops","appKey":"k"}`, []string{"--node", setNode}},
		"chat null":            {`{"chat":null}`, []string{"--node", setNode}},
		"duplicate key":        {`{"appKey":"a","appKey":"b"}`, []string{"--node", setNode}},
		"duplicate chat key":   {`{"chat":{"node":"x","node":"y"}}`, []string{"--node", setNode}},
		"trailing content":     {`{"a":1} {"b":2}`, []string{"--node", setNode}},
		"blank node":           {`{"appKey":"k"}`, []string{"--node", "  "}},
		"blank messages-loc":   {`{"appKey":"k"}`, []string{"--messages-loc", ""}},
		"bad node urn":         {`{"appKey":"k"}`, []string{"--node", "not-a-urn"}},
		"no flags":             {`{"appKey":"k"}`, nil},
		"node with memory":     {`{"appKey":"k"}`, []string{"--node", setNode, "-m", "hrn:mem:a:b"}},
		"node with messagesLc": {`{"appKey":"k"}`, []string{"--node", setNode, "--messages-loc", "x"}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			dir := chatConfigDir(t, tc.cfg)
			_, err := runChat(t, append([]string{"chat", "config", "set"}, tc.args...)...)
			if err == nil {
				t.Fatal("expected a refusal")
			}
			if got := readFile(t, cfgPath(dir)); got != tc.cfg {
				t.Errorf("file changed on refusal:\n%s", got)
			}
			if _, err := os.Stat(cfgPath(dir) + ".bak"); err == nil {
				t.Error("a backup was left behind by a refused set")
			}
		})
	}
}

func TestChatConfigSetMalformedIsUsageAndNamesNoContent(t *testing.T) {
	chatConfigDir(t, `{"appKey":"`+chatConfigSecret+`",`)
	_, err := runChat(t, "chat", "config", "set", "--node", setNode)
	wantUsage(t, err)
	if strings.Contains(err.Error(), chatConfigSecret) {
		t.Errorf("error leaked the secret: %v", err)
	}
}

func TestChatConfigSetRefusesSymlinkedConfig(t *testing.T) {
	dir := chatConfigDir(t, "")
	real := filepath.Join(dir, "real.json")
	if err := os.WriteFile(real, []byte(`{"appKey":"k"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, ".hadron"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, cfgPath(dir)); err != nil {
		t.Skip("symlinks unavailable")
	}
	_, err := runChat(t, "chat", "config", "set", "--node", setNode)
	wantUsage(t, err)
	if readFile(t, real) != `{"appKey":"k"}` {
		t.Error("wrote through the symlink")
	}
}

func TestChatConfigSetOffersNoIdentityFlags(t *testing.T) {
	chatConfigDir(t, `{}`)
	for _, fl := range []string{"--handle", "--identity", "--role", "--session", "--as"} {
		if _, err := runChat(t, "chat", "config", "set", "--node", setNode, fl, "x"); err == nil {
			t.Errorf("chat config set accepted %s", fl)
		}
	}
}

// show and doctor stay read-only: the writer is a separate verb.
func TestChatConfigShowStillWritesNothing(t *testing.T) {
	dir := chatConfigDir(t, setCfg)
	if _, err := runChat(t, "chat", "config", "show"); err != nil {
		t.Fatal(err)
	}
	if readFile(t, cfgPath(dir)) != setCfg {
		t.Error("show modified the config")
	}
	if _, err := os.Stat(cfgPath(dir) + ".bak"); err == nil {
		t.Error("show left a backup")
	}
}
