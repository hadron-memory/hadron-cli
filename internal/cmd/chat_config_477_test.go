package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// #477: `chat config show` and `chat doctor` are the READ-ONLY half of the
// stale-.hadron/config.json fix. They report what `chat read|post` would use and
// where each value came from; they never write config, post, or create a
// Channel. Writer and setup residuals stay on #477 behind their own gate.

// chatConfigSecret is an appKey value that must never surface in any output.
const chatConfigSecret = "hdr_app_SECRET_must_not_leak"

// chatConfigDir writes raw as .hadron/config.json in a fresh temp dir, chdirs
// into a subdirectory of it (the ancestor search is part of the contract), and
// clears the HADRON_CHAT_* env so the developer's own shell cannot leak in.
func chatConfigDir(t *testing.T, raw string) string {
	t.Helper()
	for _, k := range []string{"HADRON_CHAT_NODE", "HADRON_CHAT_MEMORY", "HADRON_CHAT_MESSAGES_LOC", "HADRON_CHAT_HANDLE"} {
		t.Setenv(k, "")
	}
	dir := t.TempDir()
	if raw != "" {
		if err := os.MkdirAll(filepath.Join(dir, ".hadron"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, ".hadron", "config.json"), []byte(raw), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	sub := filepath.Join(dir, "a", "b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(sub)
	return dir
}

type chatConfigInput struct {
	Key    string `json:"key"`
	Source string `json:"source"`
	Name   string `json:"name"`
	Value  string `json:"value"`
	Used   bool   `json:"used"`
}

type chatConfigShow struct {
	Config struct {
		Path    *string `json:"path"`
		Status  string  `json:"status"`
		Problem *string `json:"problem"`
		Skipped []struct {
			Path   string `json:"path"`
			Reason string `json:"reason"`
		} `json:"skipped"`
	} `json:"config"`
	Resolved *struct {
		Form        string `json:"form"`
		Memory      string `json:"memory"`
		MessagesLoc string `json:"messagesLoc"`
	} `json:"resolved"`
	Problem    *string           `json:"problem"`
	Inputs     []chatConfigInput `json:"inputs"`
	Deprecated []struct {
		Key    string `json:"key"`
		Source string `json:"source"`
		Name   string `json:"name"`
		Value  string `json:"value"`
		Effect string `json:"effect"`
	} `json:"deprecated"`
}

type chatDoctor struct {
	chatConfigShow
	Channel struct {
		Ref         *string `json:"ref"`
		Status      string  `json:"status"`
		ID          *string `json:"id"`
		Name        *string `json:"name"`
		ChatRootUrn *string `json:"chatRootUrn"`
		Lifecycle   string  `json:"lifecycle"`
	} `json:"channel"`
	OK       bool `json:"ok"`
	Problems []struct {
		Check  string `json:"check"`
		Detail string `json:"detail"`
	} `json:"problems"`
}

func runChat(t *testing.T, args ...string) (string, error) {
	t.Helper()
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(args)
	err := root.Execute()
	return out.String(), err
}

func findInput(ins []chatConfigInput, key, source string) *chatConfigInput {
	for i := range ins {
		if ins[i].Key == key && ins[i].Source == source {
			return &ins[i]
		}
	}
	return nil
}

func TestChatConfigShowReportsConfigNodeAndItsSource(t *testing.T) {
	dir := chatConfigDir(t, `{"appKey":"`+chatConfigSecret+`","handle":"iris","unrelated":{"token":"raw-unrelated"},
		"chat":{"node":"hrn:node:acme.com:tc:chats:api:messages","identity":"Claude Opus 5","role":"Backend Engineer","pollIntervalMs":10000}}`)

	// An unreachable server: show is offline by contract.
	out, err := runChat(t, "chat", "config", "show", "--json", "--server", "http://127.0.0.1:1")
	if err != nil {
		t.Fatalf("show: %v", err)
	}
	var dto chatConfigShow
	if err := json.Unmarshal([]byte(out), &dto); err != nil {
		t.Fatalf("show --json: %v (%s)", err, out)
	}
	wantPath := filepath.Join(dir, ".hadron", "config.json")
	if dto.Config.Path == nil || *dto.Config.Path != wantPath || dto.Config.Status != "loaded" {
		t.Errorf("config = %+v, want loaded %s", dto.Config, wantPath)
	}
	if dto.Resolved == nil || dto.Resolved.Form != "node" ||
		dto.Resolved.Memory != "hrn:mem:acme.com:tc" || dto.Resolved.MessagesLoc != "chats:api:messages" {
		t.Fatalf("resolved = %+v", dto.Resolved)
	}
	in := findInput(dto.Inputs, "node", "config")
	if in == nil || !in.Used || in.Name != "chat.node" {
		t.Errorf("chat.node input = %+v, want used config chat.node", in)
	}
	// The legacy identity keys are reported as no-op, never as an identity.
	got := map[string]string{}
	for _, d := range dto.Deprecated {
		if d.Effect != "none" {
			t.Errorf("deprecated %s effect = %q, want none", d.Key, d.Effect)
		}
		got[d.Key] = d.Value
	}
	if got["handle"] != "iris" || got["identity"] != "Claude Opus 5" || got["role"] != "Backend Engineer" {
		t.Errorf("deprecated = %+v", dto.Deprecated)
	}
	for _, leak := range []string{chatConfigSecret, "appKey", "raw-unrelated", "pollIntervalMs"} {
		if strings.Contains(out, leak) {
			t.Errorf("show --json printed %q: %s", leak, out)
		}
	}
}

func TestChatConfigShowHumanNeverPrintsTheAppKey(t *testing.T) {
	chatConfigDir(t, `{"appKey":"`+chatConfigSecret+`","handle":"iris","chat":{"node":"hrn:node:acme.com:tc:chats:api:messages"}}`)
	out, err := runChat(t, "chat", "config", "show", "--server", "http://127.0.0.1:1")
	if err != nil {
		t.Fatalf("show: %v", err)
	}
	if strings.Contains(out, chatConfigSecret) || strings.Contains(out, "appKey") {
		t.Errorf("human show printed the appKey: %s", out)
	}
	for _, want := range []string{"hrn:mem:acme.com:tc", "chats:api:messages", "chat.node", "handle", "no effect"} {
		if !strings.Contains(out, want) {
			t.Errorf("human show missing %q:\n%s", want, out)
		}
	}
}

// Precedence is resolveCoords' own: flag > env > config, and the node form
// (from any source) beats the two-field form.
func TestChatConfigShowPrecedence(t *testing.T) {
	chatConfigDir(t, `{"chat":{"node":"hrn:node:acme.com:cfg:chats:x:messages","memory":"hrn:mem:acme.com:cfgmem","messagesLoc":"cfg:messages"}}`)

	t.Run("env node beats config node", func(t *testing.T) {
		t.Setenv("HADRON_CHAT_NODE", "hrn:node:acme.com:env:chats:y:messages")
		out, err := runChat(t, "chat", "config", "show", "--json", "--server", "http://127.0.0.1:1")
		if err != nil {
			t.Fatal(err)
		}
		var dto chatConfigShow
		_ = json.Unmarshal([]byte(out), &dto)
		if dto.Resolved == nil || dto.Resolved.Memory != "hrn:mem:acme.com:env" {
			t.Fatalf("resolved = %+v", dto.Resolved)
		}
		if in := findInput(dto.Inputs, "node", "env"); in == nil || !in.Used || in.Name != "HADRON_CHAT_NODE" {
			t.Errorf("env input = %+v", in)
		}
		if in := findInput(dto.Inputs, "node", "config"); in == nil || in.Used {
			t.Errorf("config node must be listed and unused: %+v", in)
		}
	})

	t.Run("flag node beats env node", func(t *testing.T) {
		t.Setenv("HADRON_CHAT_NODE", "hrn:node:acme.com:env:chats:y:messages")
		out, err := runChat(t, "chat", "config", "show", "--node", "hrn:node:acme.com:flag:chats:z:messages", "--json", "--server", "http://127.0.0.1:1")
		if err != nil {
			t.Fatal(err)
		}
		var dto chatConfigShow
		_ = json.Unmarshal([]byte(out), &dto)
		if dto.Resolved == nil || dto.Resolved.Memory != "hrn:mem:acme.com:flag" || dto.Resolved.MessagesLoc != "chats:z:messages" {
			t.Fatalf("resolved = %+v", dto.Resolved)
		}
		if in := findInput(dto.Inputs, "node", "flag"); in == nil || !in.Used || in.Name != "--node" {
			t.Errorf("flag input = %+v", in)
		}
	})

	t.Run("config node beats a two-field env pair", func(t *testing.T) {
		t.Setenv("HADRON_CHAT_MEMORY", "hrn:mem:acme.com:envmem")
		t.Setenv("HADRON_CHAT_MESSAGES_LOC", "env:messages")
		out, err := runChat(t, "chat", "config", "show", "--json", "--server", "http://127.0.0.1:1")
		if err != nil {
			t.Fatal(err)
		}
		var dto chatConfigShow
		_ = json.Unmarshal([]byte(out), &dto)
		if dto.Resolved == nil || dto.Resolved.Form != "node" || dto.Resolved.Memory != "hrn:mem:acme.com:cfg" {
			t.Fatalf("resolved = %+v", dto.Resolved)
		}
		for _, in := range dto.Inputs {
			if in.Key != "node" && in.Used {
				t.Errorf("two-field input %+v must be shadowed by the node form", in)
			}
		}
	})
}

func TestChatConfigShowTwoFieldFormMixesSources(t *testing.T) {
	chatConfigDir(t, `{"chat":{"messagesLoc":"chats:api:messages:"}}`)
	out, err := runChat(t, "chat", "config", "show", "-m", "hrn:mem:acme.com:tc", "--json", "--server", "http://127.0.0.1:1")
	if err != nil {
		t.Fatal(err)
	}
	var dto chatConfigShow
	_ = json.Unmarshal([]byte(out), &dto)
	// The trailing colon is normalized off exactly as resolveCoords does.
	if dto.Resolved == nil || dto.Resolved.Form != "memory+messagesLoc" ||
		dto.Resolved.Memory != "hrn:mem:acme.com:tc" || dto.Resolved.MessagesLoc != "chats:api:messages" {
		t.Fatalf("resolved = %+v", dto.Resolved)
	}
	if in := findInput(dto.Inputs, "memory", "flag"); in == nil || !in.Used || in.Name != "--memory" {
		t.Errorf("memory input = %+v", in)
	}
	if in := findInput(dto.Inputs, "messagesLoc", "config"); in == nil || !in.Used || in.Name != "chat.messagesLoc" {
		t.Errorf("messagesLoc input = %+v", in)
	}
}

func TestChatConfigShowMissingFileIsUnresolvedNotAnError(t *testing.T) {
	chatConfigDir(t, "")
	out, err := runChat(t, "chat", "config", "show", "--json", "--server", "http://127.0.0.1:1")
	if err != nil {
		t.Fatalf("show must report, not fail: %v", err)
	}
	var dto chatConfigShow
	if err := json.Unmarshal([]byte(out), &dto); err != nil {
		t.Fatalf("%v (%s)", err, out)
	}
	if dto.Config.Status != "absent" || dto.Config.Path != nil {
		t.Errorf("config = %+v, want absent/null", dto.Config)
	}
	if dto.Resolved != nil || dto.Problem == nil || !strings.Contains(*dto.Problem, "no chat") {
		t.Errorf("resolved=%+v problem=%v", dto.Resolved, dto.Problem)
	}
	if dto.Inputs == nil || dto.Deprecated == nil {
		t.Errorf("empty lists must render as [], got %s", out)
	}
}

// A malformed file is what `chat post` silently ignores today; show names it.
func TestChatConfigShowMalformedFile(t *testing.T) {
	dir := chatConfigDir(t, `{"appKey":"`+chatConfigSecret+`", "chat": {`)
	out, err := runChat(t, "chat", "config", "show", "--json", "--server", "http://127.0.0.1:1")
	if err != nil {
		t.Fatalf("show: %v", err)
	}
	var dto chatConfigShow
	_ = json.Unmarshal([]byte(out), &dto)
	if dto.Config.Status != "malformed" || dto.Config.Path == nil || *dto.Config.Path != filepath.Join(dir, ".hadron", "config.json") || dto.Config.Problem == nil {
		t.Errorf("config = %+v", dto.Config)
	}
	if strings.Contains(out, chatConfigSecret) {
		t.Errorf("a parse error must not echo file bytes: %s", out)
	}
}

const doctorChannelJSON = `{"id":"0123456789abcdef0123456789abcdef","name":"api","description":null,"kind":"CHAT",
	"loc":"chats:api","chatRootUrn":"hrn:node:acme.com:tc:chats:api","chatRootNodeId":"n1","memoryId":"m1",
	"lastSeq":7,"lastMessageAt":"2020-01-01T00:00:00Z","createdAt":"2020-01-01T00:00:00Z","updatedAt":null,
	"memory":{"id":"m1","urn":"hrn:mem:acme.com:tc","name":"tc"}}`

func TestChatDoctorReadableChannelIsReadOnly(t *testing.T) {
	chatConfigDir(t, `{"appKey":"`+chatConfigSecret+`","chat":{"node":"hrn:node:acme.com:tc:chats:api:messages"}}`)
	gql, captured := captureGraphQL(t, map[string]string{
		"GetChannel": `{"data":{"channel":` + doctorChannelJSON + `}}`,
	})
	out, err := runChat(t, "chat", "doctor", "--json", "--server", gql.URL)
	if err != nil {
		t.Fatalf("doctor: %v (%s)", err, out)
	}
	var dto chatDoctor
	if err := json.Unmarshal([]byte(out), &dto); err != nil {
		t.Fatalf("%v (%s)", err, out)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["GetChannel"], &vars)
	if vars["ref"] != "hrn:node:acme.com:tc:chats:api" {
		t.Errorf("GetChannel ref = %v, want the chat ROOT", vars["ref"])
	}
	if !dto.OK || dto.Channel.Status != "readable" || dto.Channel.ID == nil || dto.Channel.ChatRootUrn == nil {
		t.Errorf("doctor = %+v", dto)
	}
	// The Channel carries no archived/live/superseded fact; an old lastMessageAt
	// must not be turned into one.
	if dto.Channel.Lifecycle != "not-provided" {
		t.Errorf("lifecycle = %q, want not-provided", dto.Channel.Lifecycle)
	}
	for _, word := range []string{"stale", "archived", "superseded", `"live"`} {
		if strings.Contains(out, word) {
			t.Errorf("doctor inferred %s: %s", word, out)
		}
	}
	for op := range captured {
		if op != "GetChannel" {
			t.Errorf("doctor sent %s; it may only read the Channel", op)
		}
	}
	if strings.Contains(out, chatConfigSecret) {
		t.Errorf("doctor printed the appKey")
	}
}

func TestChatDoctorUnavailableChannelExitsNotFound(t *testing.T) {
	chatConfigDir(t, `{"chat":{"node":"hrn:node:acme.com:tc:chats:api:messages"}}`)
	gql, captured := captureGraphQL(t, map[string]string{
		"GetChannel": `{"data":{"channel":null}}`,
	})
	out, err := runChat(t, "chat", "doctor", "--json", "--server", gql.URL)
	if code := exitCodeFor(err); code != exitcode.NotFound {
		t.Fatalf("exit = %d (%v), want NotFound", code, err)
	}
	var dto chatDoctor
	if err := json.Unmarshal([]byte(out), &dto); err != nil {
		t.Fatalf("the report must still print: %v (%s)", err, out)
	}
	if dto.OK || dto.Channel.Status != "unavailable" || dto.Channel.Lifecycle != "not-provided" {
		t.Errorf("doctor = %+v", dto)
	}
	if _, posted := captured["CreateChannel"]; posted {
		t.Error("doctor must never create the Channel")
	}
}

// cli#1029 BLIND (Joost, P2): a failed Channel read is a failed CHECK, not an
// early return — the report still prints, with the channel marked "error",
// and the server's mapped code is the exit only when it is the first failure.
func TestChatDoctorRefusedReadKeepsTheServersCode(t *testing.T) {
	chatConfigDir(t, `{"chat":{"node":"hrn:node:acme.com:tc:chats:api:messages"}}`)
	gql, _ := captureGraphQL(t, map[string]string{
		"GetChannel": `{"errors":[{"message":"not allowed","extensions":{"code":"FORBIDDEN"}}],"data":null}`,
	})
	out, err := runChat(t, "chat", "doctor", "--json", "--server", gql.URL)
	if code := exitCodeFor(err); code != exitcode.Forbidden {
		t.Fatalf("exit = %d (%v), want Forbidden", code, err)
	}
	var dto chatDoctor
	if err := json.Unmarshal([]byte(out), &dto); err != nil {
		t.Fatalf("the report must still print: %v (%q)", err, out)
	}
	if dto.OK || dto.Resolved == nil || dto.Channel.Status != "error" || dto.Channel.Ref == nil ||
		len(dto.Problems) != 1 || dto.Problems[0].Check != "channel" || dto.Channel.Lifecycle != "not-provided" {
		t.Errorf("doctor = %+v", dto)
	}
}

func TestChatDoctorEarlierConfigFailureOutranksARefusedRead(t *testing.T) {
	chatConfigDir(t, `{"chat": `)
	gql, _ := captureGraphQL(t, map[string]string{
		"GetChannel": `{"errors":[{"message":"not allowed","extensions":{"code":"FORBIDDEN"}}],"data":null}`,
	})
	out, err := runChat(t, "chat", "doctor", "--node", "hrn:node:acme.com:tc:chats:api:messages", "--json", "--server", gql.URL)
	if code := exitCodeFor(err); code != exitcode.Usage {
		t.Fatalf("exit = %d (%v), want Usage (the config check fails first)", code, err)
	}
	var dto chatDoctor
	if err := json.Unmarshal([]byte(out), &dto); err != nil {
		t.Fatalf("the report must still print: %v (%q)", err, out)
	}
	if len(dto.Problems) != 2 || dto.Problems[0].Check != "config" || dto.Problems[1].Check != "channel" || dto.Channel.Status != "error" {
		t.Errorf("problems = %+v channel = %+v", dto.Problems, dto.Channel)
	}
}

func TestChatDoctorUnresolvedIsUsageWithoutANetworkCall(t *testing.T) {
	chatConfigDir(t, "")
	out, err := runChat(t, "chat", "doctor", "--json", "--server", "http://127.0.0.1:1")
	if code := exitCodeFor(err); code != exitcode.Usage {
		t.Fatalf("exit = %d (%v), want Usage", code, err)
	}
	var dto chatDoctor
	if err := json.Unmarshal([]byte(out), &dto); err != nil {
		t.Fatalf("%v (%s)", err, out)
	}
	if dto.OK || dto.Channel.Status != "not-checked" || len(dto.Problems) == 0 || dto.Problems[0].Check != "coordinates" {
		t.Errorf("doctor = %+v", dto)
	}
}

// Flags win, so the channel can be readable while the file post would fall
// back to is broken; doctor still fails, because that file is silently ignored.
func TestChatDoctorMalformedConfigFailsEvenWhenFlagsResolve(t *testing.T) {
	chatConfigDir(t, `{"chat": `)
	gql, _ := captureGraphQL(t, map[string]string{
		"GetChannel": `{"data":{"channel":` + doctorChannelJSON + `}}`,
	})
	out, err := runChat(t, "chat", "doctor", "--node", "hrn:node:acme.com:tc:chats:api:messages", "--json", "--server", gql.URL)
	if code := exitCodeFor(err); code != exitcode.Usage {
		t.Fatalf("exit = %d (%v), want Usage", code, err)
	}
	var dto chatDoctor
	_ = json.Unmarshal([]byte(out), &dto)
	if dto.OK || dto.Channel.Status != "readable" || len(dto.Problems) != 1 || dto.Problems[0].Check != "config" {
		t.Errorf("doctor = %+v", dto)
	}
}

// An unreadable nearer file is walked past to an ancestor, as read and post
// do; show reports it and doctor fails on it.
func TestChatConfigUnreadableNearerFileIsSkippedAndReported(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads a 0o000 file")
	}
	dir := chatConfigDir(t, `{"chat":{"node":"hrn:node:acme.com:tc:chats:api:messages"}}`)
	near := filepath.Join(dir, "a", ".hadron", "config.json")
	if err := os.MkdirAll(filepath.Dir(near), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(near, []byte(`{"appKey":"`+chatConfigSecret+`"}`), 0o000); err != nil {
		t.Fatal(err)
	}
	out, err := runChat(t, "chat", "config", "show", "--json", "--server", "http://127.0.0.1:1")
	if err != nil {
		t.Fatal(err)
	}
	var dto chatConfigShow
	_ = json.Unmarshal([]byte(out), &dto)
	if len(dto.Config.Skipped) != 1 || dto.Config.Skipped[0].Path != near {
		t.Errorf("skipped = %+v, want %s", dto.Config.Skipped, near)
	}
	if dto.Config.Path == nil || *dto.Config.Path != filepath.Join(dir, ".hadron", "config.json") || dto.Resolved == nil {
		t.Errorf("the ancestor must still resolve: %+v / %+v", dto.Config, dto.Resolved)
	}

	gql, _ := captureGraphQL(t, map[string]string{
		"GetChannel": `{"data":{"channel":` + doctorChannelJSON + `}}`,
	})
	out, err = runChat(t, "chat", "doctor", "--json", "--server", gql.URL)
	if code := exitCodeFor(err); code != exitcode.Usage {
		t.Fatalf("exit = %d (%v), want Usage", code, err)
	}
	if strings.Contains(out, chatConfigSecret) {
		t.Error("the unreadable file's bytes leaked")
	}
}
