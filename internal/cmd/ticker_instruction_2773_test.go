package cmd

import (
	"os"

	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/hadron-memory/hadron-cli/internal/cmd/team"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#2773 (Refs #1617): --instruction on the five ticker writes, shaped as
// cli#2448 shaped it on the six App-manager writes. The flag sends
// ActInstructionInput once, exactly as given, and only when set; without it
// the plain operation goes out, byte for byte as before.

const tk2773Claim = "team-chat-message:hrn:chat:acme.com:ops:42"

type tk2773Case struct {
	name, plainOp, instrOp, field, answer string
	args                                  []string
}

func tk2773Cases() []tk2773Case {
	sub := `{"subscription":` + subscriptionFixture + `,"announcement":null}`
	return []tk2773Case{
		{"update", "UpdateTicker", "UpdateTickerInstructed", "updateTicker", tickerFixture, []string{"ticker", "update", tickerRef, "--enabled=false"}},
		{"delete", "DeleteTicker", "DeleteTickerInstructed", "deleteTicker", "true", []string{"ticker", "delete", tickerRef, "--yes"}},
		{"subscribe", "SubscribeTicker", "SubscribeTickerInstructed", "subscribeWorker", sub, []string{"ticker", "subscribe", tickerRef, "--worker", workerRef}},
		{"unsubscribe", "UnsubscribeTicker", "UnsubscribeTickerInstructed", "unsubscribeWorker", sub, []string{"ticker", "unsubscribe", tickerRef, "--worker", workerRef}},
		{"apply-templates", "ApplyAgentTickerTemplates", "ApplyAgentTickerTemplatesInstructed", "applyAgentTickerTemplates", `{"created":[],"alreadyPresent":[]}`, []string{"ticker", "apply-templates", "--worker", workerRef}},
	}
}

func tk2773Run(t *testing.T, c tk2773Case, extra ...string) (map[string]json.RawMessage, error) {
	t.Helper()
	gql, captured := captureGraphQL(t, map[string]string{
		c.plainOp: `{"data":{"` + c.field + `":` + c.answer + `}}`,
		c.instrOp: `{"data":{"` + c.field + `":` + c.answer + `}}`,
	})
	f, _ := testFactory(t)
	r := NewRootCmd(f)
	r.SetArgs(append(append(append([]string{}, c.args...), extra...), "--json", "--server", gql.URL))
	return captured, r.Execute()
}

// The flag sends the argument once, exactly as given, through the instructed
// operation and never the plain one.
func TestTickerInstructionSentOnceExactly2773(t *testing.T) {
	for _, c := range tk2773Cases() {
		t.Run(c.name, func(t *testing.T) {
			captured, err := tk2773Run(t, c, "--instruction", tk2773Claim)
			if err != nil {
				t.Fatal(err)
			}
			if _, plain := captured[c.plainOp]; plain {
				t.Fatalf("the plain %s went out with --instruction set", c.plainOp)
			}
			raw, ok := captured[c.instrOp]
			if !ok {
				t.Fatalf("%s was never sent", c.instrOp)
			}
			var v struct {
				Instruction map[string]any `json:"instruction"`
			}
			if err := json.Unmarshal(raw, &v); err != nil {
				t.Fatal(err)
			}
			if len(v.Instruction) != 2 || v.Instruction["kind"] != "TEAM_CHAT_MESSAGE" || v.Instruction["ref"] != "hrn:chat:acme.com:ops:42" {
				t.Fatalf("instruction on the wire: %s", raw)
			}
		})
	}
}

// Without the flag the plain operation goes out and carries no instruction.
func TestTickerNoFlagIsThePlainRequest2773(t *testing.T) {
	for _, c := range tk2773Cases() {
		t.Run(c.name, func(t *testing.T) {
			captured, err := tk2773Run(t, c)
			if err != nil {
				t.Fatal(err)
			}
			if _, instructed := captured[c.instrOp]; instructed {
				t.Fatalf("%s went out without --instruction", c.instrOp)
			}
			raw, ok := captured[c.plainOp]
			if !ok {
				t.Fatalf("%s was never sent", c.plainOp)
			}
			if strings.Contains(string(raw), "instruction") {
				t.Fatalf("the plain request carries an instruction: %s", raw)
			}
		})
	}
}

// A bad kind or a blank ref is refused (exit 2) before any request.
func TestTickerBadInstructionSendsNothing2773(t *testing.T) {
	for _, c := range tk2773Cases() {
		for _, bad := range []string{"bogus:ref", "task:", "task:   ", "nocolon", ""} {
			t.Run(c.name+"/"+bad, func(t *testing.T) {
				captured, err := tk2773Run(t, c, "--instruction", bad)
				if err == nil || exitCodeFor(err) != exitcode.Usage {
					t.Fatalf("want a usage refusal, got %v", err)
				}
				if len(captured) != 0 {
					t.Fatalf("a request went out before the refusal: %v", captured)
				}
			})
		}
	}
}

// A server refusal is never retried as the person or without the claim.
func TestTickerInstructionRefusalIsNotRetried2773(t *testing.T) {
	for _, c := range tk2773Cases() {
		t.Run(c.name, func(t *testing.T) {
			calls := map[string]int{}
			gql, _ := captureGraphQLFunc(t, func(op string) string {
				calls[op]++
				return `{"errors":[{"message":"manager instruction refused","extensions":{"code":"FORBIDDEN"}}]}`
			})
			f, _ := testFactory(t)
			r := NewRootCmd(f)
			r.SetArgs(append(append([]string{}, c.args...), "--instruction", tk2773Claim, "--json", "--server", gql.URL))
			if err := r.Execute(); err == nil {
				t.Fatal("a refusal must fail the command")
			}
			if calls[c.instrOp] != 1 || calls[c.plainOp] != 0 || len(calls) != 1 {
				t.Fatalf("calls %v: want exactly one %s and nothing else", calls, c.instrOp)
			}
		})
	}
}

// ticker update's "nothing to update" guard is about field flags only: the
// instruction alone is not a change.
func TestTickerUpdateInstructionAloneIsNotAChange2773(t *testing.T) {
	captured, err := tk2773Run(t, tk2773Cases()[0], "--instruction", tk2773Claim)
	_ = captured
	if err != nil {
		t.Fatal(err)
	}
	gql, got := captureGraphQL(t, nil)
	f, _ := testFactory(t)
	r := NewRootCmd(f)
	r.SetArgs([]string{"ticker", "update", tickerRef, "--instruction", tk2773Claim, "--server", gql.URL})
	if err := r.Execute(); err == nil || exitCodeFor(err) != exitcode.Usage || len(got) != 0 {
		t.Fatalf("--instruction alone must be refused as nothing to update, sending nothing: %v %v", err, got)
	}
}

// The five commands carry the shared flag and its shared help paragraph; the
// create command does not (it has no instruction argument).
func TestTickerInstructionHelp2773(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	for _, path := range [][]string{{"ticker", "update"}, {"ticker", "delete"}, {"ticker", "subscribe"}, {"ticker", "unsubscribe"}, {"ticker", "apply-templates"}} {
		c, _, err := root.Find(path)
		if err != nil || c.Flags().Lookup("instruction") == nil {
			t.Errorf("%v: no --instruction flag (%v)", path, err)
			continue
		}
		if !strings.Contains(c.Long, "is for an AI worker acting on its holder's say-so") {
			t.Errorf("%v: the shared instruction help is missing", path)
		}
	}
	if c, _, _ := root.Find([]string{"ticker", "create"}); c == nil || c.Flags().Lookup("instruction") != nil {
		t.Error("ticker create has no instruction argument and must not offer the flag")
	}
}

// The five plain documents are pinned by literal hash: adding the instructed
// twins must not move what a no-flag call sends.
func TestTickerPlainDocumentsUnchanged2773(t *testing.T) {
	want := map[string]string{}
	for op, h := range tk2773PlainHashes {
		want[op] = h
	}
	got := map[string]string{
		"UpdateTicker":              gen.UpdateTicker_Operation,
		"DeleteTicker":              gen.DeleteTicker_Operation,
		"SubscribeTicker":           gen.SubscribeTicker_Operation,
		"UnsubscribeTicker":         gen.UnsubscribeTicker_Operation,
		"ApplyAgentTickerTemplates": gen.ApplyAgentTickerTemplates_Operation,
	}
	for op, q := range got {
		sum := sha256.Sum256([]byte(q))
		if h := hex.EncodeToString(sum[:]); h != want[op] {
			t.Errorf("%s document moved: sha256 %s, want %s", op, h, want[op])
		}
	}
}

// Captured from the generated plain operations on origin/main before the
// instructed operations were added: the SHA-256 of each gen.<Op>_Operation text.
var tk2773PlainHashes = map[string]string{
	"UpdateTicker":              "714d926c920e89f158cf2f99f5a2afa9e267324f9c9bc3f3027e440546733a91",
	"DeleteTicker":              "7db3562fa284bd2cb49e825f58b8ab78dce7d16e60a834ebcd4fd8e3d5b6eb27",
	"SubscribeTicker":           "44d63a7ea2660a798251368a00bc5abc8e1389851be1473e713baf92ef4efb7d",
	"UnsubscribeTicker":         "4fa43b22e79d04540eb100969cc8c5cfcdf8e44a566a3c7f9549766ff0fd1617",
	"ApplyAgentTickerTemplates": "aa0660fc25e013675032dc0c8ceec177fa32e624586e827c0401922c9916ae34",
}

// In a worktree with a verified binding the instructed call goes out through
// the worker's session; the plain call never carries one, and an unusable
// binding (other server) sends the claim as the person with a note.
func TestTickerInstructionSessionAttribution2773(t *testing.T) {
	for _, c := range tk2773Cases() {
		for _, state := range []string{"verified", "other-server"} {
			t.Run(c.name+"/"+state, func(t *testing.T) {
				teamGitDir(t)
				srv, calls := snapshotServer1614(t, func(call snapshotCall1614) string {
					if call.OperationName == "GetBoundSessionEnd" {
						return `{"data":{"session":{"id":"s-new","endedAt":null,"endReason":null}}}`
					}
					return `{"data":{"` + c.field + `":` + c.answer + `}}`
				})
				writeTeamBindingIn(t, os.Getenv(team.GitDirEnv), srv.URL)
				if state == "other-server" {
					rewriteBinding1592(t, func(b map[string]any) { b["server"] = "https://other.invalid" })
				}
				run := func(extra ...string) (string, string) {
					*calls = (*calls)[:0]
					f, _ := testFactory(t)
					root := NewRootCmd(f)
					root.SetArgs(append(append(append([]string{}, c.args...), extra...), "--json", "--server", srv.URL))
					if err := root.Execute(); err != nil {
						t.Fatal(err)
					}
					var session, op string
					for _, call := range *calls {
						if call.OperationName == c.plainOp || call.OperationName == c.instrOp {
							session, op = call.Session, call.OperationName
						}
					}
					return op, session + "|" + f.IOStreams.ErrOut.(*strings.Builder).String()
				}
				if op, got := run(); op != c.plainOp || strings.Split(got, "|")[0] != "" {
					t.Fatalf("a no-flag call must be the plain operation without a worker session: %s %q", op, got)
				}
				op, got := run("--instruction", tk2773Claim)
				if op != c.instrOp {
					t.Fatalf("instructed call went out as %s", op)
				}
				session, note, _ := strings.Cut(got, "|")
				if state == "verified" && session != "s-new" {
					t.Fatalf("a verified binding must carry its session, got %q", session)
				}
				if state == "other-server" && (session != "" || note == "") {
					t.Fatalf("an unusable binding goes out as the person with a note: session %q note %q", session, note)
				}
			})
		}
	}
}

// TickerPatch is one generated type shared by the plain and instructed update:
// an omitted field must stay omitted on BOTH, never an explicit null (which the
// server reads as "clear"). Only the fields given are on the wire.
func TestTickerUpdateInstructedOmitsUnsetFields2773(t *testing.T) {
	c := tk2773Cases()[0]
	c.args = []string{"ticker", "update", tickerRef, "--enabled=false"}
	captured, err := tk2773Run(t, c, "--instruction", tk2773Claim)
	if err != nil {
		t.Fatal(err)
	}
	var v struct{ Input map[string]any }
	if err := json.Unmarshal(captured[c.instrOp], &v); err != nil {
		t.Fatal(err)
	}
	if len(v.Input) != 1 || v.Input["enabled"] != false {
		t.Fatalf("instructed update wire input %v, want only enabled=false", v.Input)
	}
}
