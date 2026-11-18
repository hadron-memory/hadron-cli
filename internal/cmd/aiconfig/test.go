package aiconfig

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/api/gqltypes"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

type testErrorDTO struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Transient bool   `json:"transient"`
}
type testUsedDTO struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	Endpoint string `json:"endpoint"`
	APIStyle string `json:"apiStyle"`
	Key      string `json:"key"`
	Params   string `json:"params"`
}
type testKnobDTO struct {
	Param  string  `json:"param"`
	State  string  `json:"state"`
	Detail *string `json:"detail"`
}
type testConfigDTO struct {
	OK        bool          `json:"ok"`
	Provider  string        `json:"provider"`
	Model     string        `json:"model"`
	Endpoint  *string       `json:"endpoint"`
	APIStyle  *string       `json:"apiStyle"`
	LatencyMS int           `json:"latencyMs"`
	Reply     *string       `json:"reply"`
	Error     *testErrorDTO `json:"error"`
	Used      testUsedDTO   `json:"used"`
	Knobs     []testKnobDTO `json:"knobs"`
	// Chat is the chat probe's closed status, verbatim (an unknown future
	// value included), or null when no probe was requested (--chat-probe).
	Chat *string `json:"chat"`
}

func newCmdTest(f *cmdutil.Factory) *cobra.Command {
	var overrideFile string
	var chatProbe bool
	cmd := &cobra.Command{
		Use: "test <id>", Short: "Test a saved AI config with one small provider call",
		Long: `Test a saved AI config by id with one small real call to its provider, made
through the same client and endpoint a run would use. The stored key stays on
the server and nothing is saved. Every call that reaches the provider is
metered and may cost money. The server applies the same owner permission as
update and refuses impersonation. NO_API_KEY (no key stored) and KEY_UNREADABLE
(the stored key cannot be decrypted) make no provider call.

--override-file tries edited values without saving them: a JSON object with
optional provider, model, apiKey, endpoint, apiStyle and params. Use a private
file, or - for piped JSON, to keep keys out of argv. A field you leave out uses
the saved value; null for apiStyle or endpoint uses the provider default; null
params clears the edited params; params, when present, is an object. The
override is never saved or printed, and a piped document is refused at a
terminal.

Output: ok, the provider, model, endpoint and style actually used, latencyMs,
the model's reply, the error (provider text removed) if any, where each value
came from (used), and the result of each provider knob in the params. A test that
completed exits 0 even when ok is false: read ok and error.code in --json. A
refused request uses the usual CLI exit codes. On KEY_UNREADABLE, store the key
again with 'hadron ai-config update <id> --api-key -'. Retry a provider failure
only when error.transient says you may.

--chat-probe (off by default) adds a second, separately metered provider call
that checks the model can answer a chat turn the way Hadron needs: by calling
the chat "respond" tool exactly once. It counts against your allowance when a
platform key pays for it, and needs a server that supports it (an older server
refuses it with exit 2). The result prints on its own "chat:" line and is the
"chat" key in --json (null when not requested). The run result and the chat
status are two outcomes: neither stands for the other. A test that completed
exits 0 whatever ok and the chat status say; request refusals, an invalid
response from the server and other errors (argument, override-file and
transport failures) use the usual CLI error exits. Statuses:
  PASS                       the model called respond once; usable for chat
  FAIL_TEXT_ONLY             it answered in plain text, no tool call
  FAIL_OTHER_TOOL            it called a tool other than respond
  FAIL_MULTIPLE_CALLS        it called respond more than once
  FAIL_MALFORMED             the call's arguments were not valid
  FAIL_PROVIDER_UNAVAILABLE  the provider call itself failed; retry or fix ok
  UNSUPPORTED_PROVIDER       the server cannot probe this provider
After any FAIL, pick a different model or provider ('ai-config tool-choice'
predicts how chat treats a pair; this probe checks it for real).`,
		Example: "  hadron ai-config test cfg_123 --json\n  hadron ai-config test cfg_123 --override-file edited-config.json --json\n  hadron ai-config test cfg_123 --chat-probe --json",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var override *gqltypes.AiServiceConfigTestOverride
			var err error
			if cmd.Flags().Changed("override-file") {
				if overrideFile == "-" {
					if err := cmdutil.RefuseDocumentStdinFromTerminal(f.IOStreams.IsInputTerminal(), "--override-file -", "--override-file"); err != nil {
						return err
					}
				}
				override, err = loadTestOverride(overrideFile, f.IOStreams.In)
				if err != nil {
					return err
				}
			}
			var submittedKey string
			if override != nil {
				_ = json.Unmarshal(override.APIKey, &submittedKey) // validated string/null by loadTestOverride
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			var v *testResult
			if chatProbe {
				r, err := gen.TestAiServiceConfigWithChatProbe(cmd.Context(), client, args[0], true, override)
				if err != nil {
					return redactTestError(api.MapError(err), submittedKey)
				}
				v = fromProbeTestResult(r.TestAiServiceConfig)
			} else {
				r, err := gen.TestAiServiceConfig(cmd.Context(), client, args[0], override)
				if err != nil {
					return redactTestError(api.MapError(err), submittedKey)
				}
				v = fromDefaultTestResult(r.TestAiServiceConfig)
			}
			if v == nil || v.Used == nil {
				return exitcode.Newf(exitcode.Error, "server returned no AI config test result or value sources")
			}
			dto := testConfigDTO{OK: v.Ok, Provider: v.Provider, Model: v.Model, Endpoint: v.Endpoint, APIStyle: v.APIStyle, LatencyMS: v.LatencyMs, Reply: v.Reply,
				Used: *v.Used, Knobs: knobDTOs(v.Knobs)}
			if chatProbe {
				if v.Chat == nil {
					return exitcode.Newf(exitcode.Error, "server returned no chat probe status though one was requested")
				}
				dto.Chat = v.Chat
			}
			if v.Error != nil {
				dto.Error = &testErrorDTO{Code: v.Error.Code, Message: v.Error.Message, Transient: v.Error.Transient}
			}
			redactTestResult(&dto, submittedKey)
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				state := "failed"
				if dto.OK {
					state = "ok"
				}
				if _, err := fmt.Fprintf(w, "%s  %s/%s  %d ms\nendpoint: %s\nAPI style: %s\n", state, output.SafeCell(dto.Provider), output.SafeCell(dto.Model), dto.LatencyMS, output.SafeCell(endpointDisplay(dto.Endpoint, "unknown")), output.SafeCell(endpointDisplay(dto.APIStyle, "single-protocol provider"))); err != nil {
					return err
				}
				if dto.Error != nil {
					if _, err := fmt.Fprintf(w, "%s: %s (transient: %t)\n", output.SafeCell(dto.Error.Code), output.SafeCell(dto.Error.Message), dto.Error.Transient); err != nil {
						return err
					}
				}
				if dto.Reply != nil {
					if _, err := fmt.Fprintf(w, "reply: %s\n", output.SafeCell(*dto.Reply)); err != nil {
						return err
					}
				}
				if _, err := fmt.Fprintf(w, "used: provider=%s model=%s endpoint=%s apiStyle=%s key=%s params=%s\n", output.SafeCell(dto.Used.Provider), output.SafeCell(dto.Used.Model), output.SafeCell(dto.Used.Endpoint), output.SafeCell(dto.Used.APIStyle), output.SafeCell(dto.Used.Key), output.SafeCell(dto.Used.Params)); err != nil {
					return err
				}
				for _, k := range dto.Knobs {
					detail := output.SafeCell(endpointDisplay(k.Detail, ""))
					if detail != "" {
						detail = " " + detail
					}
					if _, err := fmt.Fprintf(w, "%s: %s%s\n", output.SafeCell(k.Param), output.SafeCell(k.State), detail); err != nil {
						return err
					}
				}
				if dto.Chat != nil {
					// Its own line, apart from the run state above: a chat status never
					// folds into, or stands for, the run result.
					if _, err := fmt.Fprintf(w, "chat: %s\n", output.SafeCell(*dto.Chat)); err != nil {
						return err
					}
				}
				return nil
			})
		},
	}
	cmd.Flags().BoolVar(&chatProbe, "chat-probe", false, "also run one chat probe: a second, separately metered provider call; its status prints apart from the run result")
	cmd.Flags().StringVar(&overrideFile, "override-file", "", "edited values as JSON; - reads a pipe; never saved or echoed")
	return cmd
}

// knobDTOs copies the knob entries into DTOs and skips a nil entry. Both typed
// adapters already filter nils, so this changes nothing today; it keeps the
// dereference safe if a future adapter does not (cli#2364 item 4). An empty
// result is a non-nil slice, so --json renders [] and never null.
func knobDTOs(knobs []*testKnobDTO) []testKnobDTO {
	out := make([]testKnobDTO, 0, len(knobs))
	for _, k := range knobs {
		if k == nil {
			continue
		}
		out = append(out, *k)
	}
	return out
}

func loadTestOverride(path string, in io.Reader) (*gqltypes.AiServiceConfigTestOverride, error) {
	var data []byte
	var err error
	if path == "-" {
		data, err = io.ReadAll(in)
	} else {
		data, err = os.ReadFile(path)
	}
	if err != nil {
		return nil, exitcode.Newf(exitcode.Usage, "cannot read --override-file")
	}
	data = bytes.TrimSpace(data)
	if len(data) == 0 || data[0] != '{' {
		return nil, exitcode.Newf(exitcode.Usage, "--override-file must contain one JSON object")
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var v gqltypes.AiServiceConfigTestOverride
	if err := dec.Decode(&v); err != nil {
		return nil, exitcode.Newf(exitcode.Usage, "invalid --override-file JSON or unsupported field")
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, exitcode.Newf(exitcode.Usage, "--override-file must contain exactly one JSON object")
	}
	for _, raw := range []json.RawMessage{v.Provider, v.Model, v.APIKey, v.Endpoint, v.APIStyle} {
		if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			continue
		}
		var s string
		if json.Unmarshal(raw, &s) != nil {
			return nil, exitcode.Newf(exitcode.Usage, "override provider, model, apiKey, endpoint and apiStyle must be strings or null")
		}
	}
	if len(v.APIStyle) > 0 && !bytes.Equal(bytes.TrimSpace(v.APIStyle), []byte("null")) {
		var s string
		_ = json.Unmarshal(v.APIStyle, &s)
		if s != "CHAT_COMPLETIONS" && s != "RESPONSES" {
			return nil, exitcode.Newf(exitcode.Usage, "override apiStyle must be CHAT_COMPLETIONS, RESPONSES or null")
		}
	}
	if raw := bytes.TrimSpace(v.Params); len(raw) > 0 && raw[0] != '{' && !bytes.Equal(raw, []byte("null")) {
		return nil, exitcode.Newf(exitcode.Usage, "override params must be an object or null")
	}
	return &v, nil
}

// redactTestError detaches every mapped error when a key was submitted: a
// separately rendered metadata field can echo it even when Error() is clean.
// Classification happens before this call. Detached retains the redacted
// public face and sentinels without retaining raw GraphQL/HTTP causes.
func redactTestError(err error, key string) error {
	if err == nil || key == "" {
		return err
	}
	return api.Detached(err, func(text string) string { return redactSubmittedKey(text, key) })
}

// Only the submitted override is known here; saved keys never reach the CLI.
// Old coercion diagnostics can quote the key, so cover literal and JSON/Go
// string-escaped spellings without changing the submitted request.
func redactSubmittedKey(message, key string) string {
	if key == "" {
		return message
	}
	quoted, _ := json.Marshal(key)
	goQuoted := strconv.Quote(key)
	forms := []string{key, string(quoted[1 : len(quoted)-1]), goQuoted[1 : len(goQuoted)-1]}
	sort.SliceStable(forms, func(i, j int) bool { return len(forms[i]) > len(forms[j]) })
	replacement := "[redacted]"
	if strings.Contains(replacement, key) {
		replacement = ""
	}
	pairs := make([]string, 0, len(forms)*2)
	for _, form := range forms {
		pairs = append(pairs, form, replacement)
	}
	return strings.NewReplacer(pairs...).Replace(message)
}

// testResult is what the command renders from either test document. The
// default and chat-probe documents are distinct generated types (the probe
// adds the chat selection), so each has its own typed adapter below: a field
// renamed or retyped in either document is a compile error here. An added
// field still needs an explicit mapping; typed adapters do not enforce that
// additions reach the stable JSON DTO (cli#1959).
type testResult struct {
	Ok        bool
	Provider  string
	Model     string
	Endpoint  *string
	APIStyle  *string
	LatencyMs int
	Reply     *string
	Error     *testErrorDTO
	Used      *testUsedDTO
	Knobs     []*testKnobDTO
	Chat      *string // nil when the document has no chat selection, or the server sent none
}

type (
	defaultTestResult = gen.TestAiServiceConfigTestAiServiceConfigAiServiceConfigTestResult
	probeTestResult   = gen.TestAiServiceConfigWithChatProbeTestAiServiceConfigAiServiceConfigTestResult
)

// testResultFields is the scalar projection shared by both typed documents.
// The nested generated types differ, but their same-shaped fields can be
// explicitly converted; a rename or retype makes that conversion fail to build.
type testResultFields interface {
	GetOk() bool
	GetProvider() string
	GetModel() string
	GetEndpoint() *string
	GetApiStyle() *gen.AiApiStyle
	GetLatencyMs() int
	GetReply() *string
}

type (
	defaultTestError = gen.TestAiServiceConfigTestAiServiceConfigAiServiceConfigTestResultErrorAiServiceConfigTestError
	defaultTestUsed  = gen.TestAiServiceConfigTestAiServiceConfigAiServiceConfigTestResultUsedAiServiceConfigTestUsed
	defaultTestKnob  = gen.TestAiServiceConfigTestAiServiceConfigAiServiceConfigTestResultKnobsAiConfigTestKnob
)

func fromDefaultTestResult(r *defaultTestResult) *testResult {
	if r == nil {
		return nil
	}
	return fromTestResultFields(r, r.Error, r.Used, r.Knobs)
}

func fromProbeTestResult(r *probeTestResult) *testResult {
	if r == nil {
		return nil
	}
	knobs := make([]*defaultTestKnob, 0, len(r.Knobs))
	for _, k := range r.Knobs {
		knobs = append(knobs, (*defaultTestKnob)(k))
	}
	v := fromTestResultFields(r, (*defaultTestError)(r.Error), (*defaultTestUsed)(r.Used), knobs)
	if r.Chat != nil {
		status := string(r.Chat.Status) // verbatim, an unknown future value included
		v.Chat = &status
	}
	return v
}

func fromTestResultFields(r testResultFields, e *defaultTestError, u *defaultTestUsed, knobs []*defaultTestKnob) *testResult {
	v := &testResult{Ok: r.GetOk(), Provider: r.GetProvider(), Model: r.GetModel(), Endpoint: r.GetEndpoint(), APIStyle: apiStyleString(r.GetApiStyle()), LatencyMs: r.GetLatencyMs(), Reply: r.GetReply(), Knobs: make([]*testKnobDTO, 0, len(knobs))}
	if e != nil {
		v.Error = &testErrorDTO{Code: e.Code, Message: e.Message, Transient: e.Transient}
	}
	if u != nil {
		v.Used = &testUsedDTO{Provider: string(u.Provider), Model: string(u.Model), Endpoint: string(u.Endpoint), APIStyle: string(u.ApiStyle), Key: string(u.Key), Params: string(u.Params)}
	}
	// Only non-nil entries enter the command DTO's dereference loop.
	for _, k := range knobs {
		if k != nil {
			v.Knobs = append(v.Knobs, &testKnobDTO{Param: k.Param, State: k.State, Detail: k.Detail})
		}
	}
	return v
}

// redactTestResult masks submitted-key echoes in every rendered string value,
// including a provider that wrongly places the echo outside its error fields.
// JSON field names, nulls, numeric/boolean values and the sent request are intact.
// Saved keys are unknown to the CLI; the server must keep those masked itself.
func redactTestResult(dto *testConfigDTO, key string) {
	if key == "" {
		return
	}
	redact := func(value string) string { return redactSubmittedKey(value, key) }
	redactPtr := func(value *string) *string {
		if value == nil {
			return nil
		}
		safe := redact(*value)
		return &safe
	}
	dto.Provider = redact(dto.Provider)
	dto.Model = redact(dto.Model)
	dto.Endpoint = redactPtr(dto.Endpoint)
	dto.APIStyle = redactPtr(dto.APIStyle)
	dto.Reply = redactPtr(dto.Reply)
	dto.Chat = redactPtr(dto.Chat)
	if dto.Error != nil {
		dto.Error.Code = redact(dto.Error.Code)
		dto.Error.Message = redact(dto.Error.Message)
	}
	dto.Used.Provider = redact(dto.Used.Provider)
	dto.Used.Model = redact(dto.Used.Model)
	dto.Used.Endpoint = redact(dto.Used.Endpoint)
	dto.Used.APIStyle = redact(dto.Used.APIStyle)
	dto.Used.Key = redact(dto.Used.Key)
	dto.Used.Params = redact(dto.Used.Params)
	for i := range dto.Knobs {
		dto.Knobs[i].Param = redact(dto.Knobs[i].Param)
		dto.Knobs[i].State = redact(dto.Knobs[i].State)
		dto.Knobs[i].Detail = redactPtr(dto.Knobs[i].Detail)
	}
}

func apiStyleString(s *gen.AiApiStyle) *string {
	if s == nil {
		return nil
	}
	v := string(*s)
	return &v
}
