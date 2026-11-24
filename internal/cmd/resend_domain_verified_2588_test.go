package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/parser"
)

// cli#2588 (server half hadron-server#4865): `org resend-sender get` reports whether Resend says the
// domain of the sender's address is verified. REPORT ONLY (Holger's ruling, team chat #138478): a read
// field, no test-send and no verification change. The server reads it live from Resend on every selection
// (one tool call per sender, bounded at 12 s) and answers true, false, or null (could not be read, which
// "says nothing about the domain"), so the CLI shows three states and asks for it only where it is shown.

const (
	senderBase2588    = `"id":"sender1","organizationId":"org1","from":"Robot <robot@example.test>","enabled":true,"hasApiKey":true,"createdAt":"2026-10-31T00:00:00Z","updatedAt":"2026-10-31T00:00:00Z","createdBy":null,"updatedBy":null`
	senderGetOp2588   = "GetResendSender"
	senderGetBody2588 = `{"data":{"resendSender":{` + senderBase2588 + `%s}}}`
)

func getSenderWith2588(t *testing.T, extra string, jsonOut bool) string {
	t.Helper()
	body := strings.Replace(senderGetBody2588, "%s", extra, 1)
	srv, _ := captureGraphQL(t, map[string]string{senderGetOp2588: body})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	args := []string{"org", "resend-sender", "get", "sender1", "--server", srv.URL}
	if jsonOut {
		args = append(args, "--json")
	}
	root.SetArgs(args)
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

// The three states, in --json: true and false are booleans; the server's null (could not be read) stays a
// PRESENT key with a null value, never omitted and never false: a false would claim the domain is not
// verified when nobody could ask. A server that omits the field entirely reads the same as null.
// Kills: a DTO that drops the field, a bool (not pointer) DTO that turns null into false, omitempty.
func TestResendSenderGetJSONCarriesDomainVerifiedThreeStates2588(t *testing.T) {
	for _, tc := range []struct {
		name, extra string
		want        any
	}{
		{"verified", `,"domainVerified":true`, true},
		{"not verified", `,"domainVerified":false`, false},
		{"could not be read", `,"domainVerified":null`, nil},
		{"field absent from the answer", ``, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var dto map[string]any
			if err := json.Unmarshal([]byte(getSenderWith2588(t, tc.extra, true)), &dto); err != nil {
				t.Fatal(err)
			}
			v, present := dto["domainVerified"]
			if !present {
				t.Fatalf("domainVerified must be present (null when unknown): %v", dto)
			}
			if v != tc.want {
				t.Fatalf("domainVerified = %v, want %v", v, tc.want)
			}
			// the nine existing keys are unchanged
			if len(dto) != 10 || dto["hasApiKey"] != true || dto["enabled"] != true {
				t.Fatalf("the get DTO is the nine existing keys plus domainVerified: %v", dto)
			}
		})
	}
}

// The text view names the state in words and, for "no" and "unknown", says what it means: a no is Resend's
// report (it includes "Resend has no record of the domain"); an unknown says nothing about the domain.
// Kills: printing a bool as true/false, folding unknown into no, dropping the line.
func TestResendSenderGetTextSaysTheDomainState2588(t *testing.T) {
	for _, tc := range []struct{ name, extra, want string }{
		{"verified", `,"domainVerified":true`, "  domain verified: yes\n"},
		{"not verified", `,"domainVerified":false`, "  domain verified: no (Resend reports it not verified, or has no record of the domain)\n"},
		{"unknown", `,"domainVerified":null`, "  domain verified: unknown (could not be read; says nothing about the domain)\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := getSenderWith2588(t, tc.extra, false)
			if !strings.Contains(out, tc.want) {
				t.Fatalf("text must contain %q:\n%s", tc.want, out)
			}
			if strings.Contains(out, "unknown") == (tc.name != "unknown") {
				t.Fatalf("only the null state reads unknown:\n%s", out)
			}
		})
	}
}

// The server reads the state live from Resend once per sender each time the field is selected, so only
// `get` asks for it: list would cost a tool call per listed sender, and create and update would wait on a
// lookup nobody shows. Parses the operations the client actually sends.
// Kills: moving domainVerified into the shared ResendSenderFields fragment (every sender operation then
// selects it), or dropping it from the get operation.
func TestResendSenderDomainVerifiedIsSelectedOnlyByGet2588(t *testing.T) {
	for _, tc := range []struct {
		name, op string
		want     bool
	}{
		{"get", gen.GetResendSender_Operation, true},
		{"list", gen.ListResendSenders_Operation, false},
		{"create", gen.CreateResendSender_Operation, false},
		{"update", gen.UpdateResendSender_Operation, false},
		{"rm", gen.DeleteResendSender_Operation, false},
	} {
		doc, err := parser.ParseQuery(&ast.Source{Name: tc.name, Input: tc.op})
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		got := selectsField2588(doc, "domainVerified")
		if got != tc.want {
			t.Errorf("%s operation selects domainVerified = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func selectsField2588(doc *ast.QueryDocument, name string) bool {
	var walk func(ast.SelectionSet) bool
	walk = func(set ast.SelectionSet) bool {
		for _, s := range set {
			switch s := s.(type) {
			case *ast.Field:
				if s.Name == name || walk(s.SelectionSet) {
					return true
				}
			case *ast.InlineFragment:
				if walk(s.SelectionSet) {
					return true
				}
			case *ast.FragmentSpread:
				if def := doc.Fragments.ForName(s.Name); def != nil && walk(def.SelectionSet) {
					return true
				}
			}
		}
		return false
	}
	for _, op := range doc.Operations {
		if walk(op.SelectionSet) {
			return true
		}
	}
	return false
}

// The other sender commands print the nine keys they always did (no domainVerified: it was not asked for,
// and a null there would read as "could not be read").
func TestResendSenderOtherCommandsDoNotCarryDomainVerified2588(t *testing.T) {
	for _, tc := range []struct {
		op, field string
		args      []string
	}{
		{"UpdateResendSender", "updateResendSender", []string{"update", "sender1", "--enabled=false"}},
		{"CreateResendSender", "createResendSender", []string{"create", "--org", "acme.com", "--from", "Robot <robot@example.test>", "--api-key-file", "-"}},
	} {
		srv, _ := captureGraphQL(t, map[string]string{tc.op: `{"data":{"` + tc.field + `":{` + senderBase2588 + `}}}`})
		f, out := testFactory(t)
		f.IOStreams.In = strings.NewReader("synthetic-key-2588\n")
		root := NewRootCmd(f)
		root.SetArgs(append(append([]string{"org", "resend-sender"}, tc.args...), "--json", "--server", srv.URL))
		if err := root.Execute(); err != nil {
			t.Fatal(err)
		}
		var dto map[string]any
		if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
			t.Fatal(err)
		}
		if _, has := dto["domainVerified"]; has || len(dto) != 9 {
			t.Fatalf("%s prints the nine existing keys only: %v", tc.op, dto)
		}
	}
}

// The help says what the line is, and only that: Resend's own answer, read live, report only. It must not
// promise a test send or a way to verify (the ruling's "no test-send", and the FIT: help text must not
// promise either), and says unknown "says nothing about the domain".
// Kills: help that omits the field, claims a cache, or promises a send or a verify.
func TestResendSenderGetHelpDescribesTheDomainLine2588(t *testing.T) {
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"org", "resend-sender", "get", "--help"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	help := strings.Join(strings.Fields(out.String()), " ")
	for _, w := range []string{
		"domain verified",
		"Resend's answer for the domain of the sender address",
		"read live on each get",
		"yes, no (Resend reports it not verified, or has no record of the domain), or unknown",
		"unknown says nothing about the domain",
		"get sends no mail and changes nothing at Resend",
	} {
		if !strings.Contains(help, w) {
			t.Errorf("get --help must say %q:\n%s", w, help)
		}
	}
	for _, bad := range []string{"test send", "test-send", "send a test", "cached", "verifies the domain for you"} {
		if strings.Contains(help, bad) {
			t.Errorf("get --help must not say %q", bad)
		}
	}
	// the entry in the agent contract names the field and its three states
	raw := string(readContract(t))
	i := strings.Index(raw, "`org resend-sender` manages")
	if i < 0 {
		t.Fatal("agentic-usage has no org resend-sender section")
	}
	section := strings.Join(strings.Fields(raw[i:min(len(raw), i+2600)]), " ")
	for _, w := range []string{"domainVerified", "true, false or null", "read live from Resend", "report only"} {
		if !strings.Contains(section, w) {
			t.Errorf("the agentic-usage resend-sender section must say %q", w)
		}
	}
}
