package cmd

import (
	"fmt"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#1955: a server older than the signing preview's agentUrn/workerUrn fields
// refuses the OrgBootPromptSigningPreview document. Only that exact refusal gets
// the actionable sentence; every other failure keeps its existing path. The
// refusal happens after exactly ONE preview request, with no retry, no fallback
// document and, on `on`, no signing mutation.

const previewOp = "OrgBootPromptSigningPreview"

// valErr is one GraphQL validation error naming a field the server lacks.
func valErr(field, parent string) string {
	return fmt.Sprintf(`{"message":%q,"extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}`,
		fmt.Sprintf(`Cannot query field "%s" on type "%s".`, field, parent))
}

func errsResp(errs ...string) string {
	return `{"errors":[` + strings.Join(errs, ",") + `],"data":null}`
}

// oldServerBody is what a server without the two fields answers: BOTH names are
// reported, as the validator lists every unknown field.
func oldServerBody() string {
	return errsResp(valErr("agentUrn", "OrgBootPromptSigningPreviewRow"), valErr("workerUrn", "OrgBootPromptSigningPreviewRow"))
}

func fakeAnswering(t *testing.T, previewBody string) *sigFake {
	return newSigFake(t, func(op string, _ int, _ map[string]any) string {
		if op == previewOp {
			return previewBody
		}
		return setResp(true, true, true)
	})
}

// assertOneRefusedPreview pins the request shape common to every refusal path.
func assertOneRefusedPreview(t *testing.T, fake *sigFake) {
	t.Helper()
	if n := len(fake.requests(previewOp)); n != 1 {
		t.Errorf("preview requests = %d, want exactly 1 (no retry, no fallback); ops = %v", n, fake.ops())
	}
	if got := fake.ops(); len(got) != 1 {
		t.Errorf("ops = %v, want only the one preview request (no probe, no other document)", got)
	}
	if n := len(fake.requests("SetOrgBootPromptSigning")); n != 0 {
		t.Errorf("signing mutation sent %d time(s) after a failed preview", n)
	}
}

func TestOrgSigningOlderServerMissingURNFields(t *testing.T) {
	for _, status := range []string{"", "HTTP400 "} {
		for _, args := range [][]string{
			{"org", "signing", "preview", "acme.com"},
			{"org", "signing", "preview", "acme.com", "--all"},
			{"org", "signing", "on", "acme.com", "--yes"},
		} {
			name := strings.Join(args[2:], " ") + "/status=" + strings.TrimSpace(status)
			t.Run(name, func(t *testing.T) {
				fake := fakeAnswering(t, status+oldServerBody())
				f, out, errOut := testFactoryTTY(t, "")
				if code := runSigning(t, f, fake, args...); code != exitcode.Usage {
					t.Fatalf("exit %d, want 2", code)
				}
				all := out.String() + errOut.String()
				for _, want := range []string{"agentUrn", "workerUrn", "cac6c05c6", "no signing change was sent"} {
					if !strings.Contains(all, want) {
						t.Errorf("message lacks %q:\n%s", want, all)
					}
				}
				for _, bad := range []string{"out of sync", "Nothing was sent", "nothing was sent"} {
					if strings.Contains(all, bad) {
						t.Errorf("message must not say %q:\n%s", bad, all)
					}
				}
				assertOneRefusedPreview(t, fake)
			})
		}
	}
}

// Only ONE of the two names reported (a server that has one of them) still
// qualifies: every parsed error names a field of this change.
func TestOrgSigningOlderServerSingleMissingField(t *testing.T) {
	fake := fakeAnswering(t, "HTTP400 "+errsResp(valErr("workerUrn", "OrgBootPromptSigningPreviewRow")))
	f, out, errOut := testFactoryTTY(t, "")
	if code := runSigning(t, f, fake, "org", "signing", "preview", "acme.com"); code != exitcode.Usage {
		t.Fatalf("exit %d, want 2", code)
	}
	if all := out.String() + errOut.String(); !strings.Contains(all, "cac6c05c6") {
		t.Errorf("message lacks the minimum commit:\n%s", all)
	}
	assertOneRefusedPreview(t, fake)
}

func TestOrgSigningOlderServerJSONKeepsFrameworkCode(t *testing.T) {
	fake := fakeAnswering(t, "HTTP400 "+oldServerBody())
	f, out, errOut := testFactoryTTY(t, "")
	if code := runSigning(t, f, fake, "org", "signing", "preview", "acme.com", "--json"); code != exitcode.Usage {
		t.Fatalf("exit %d, want 2", code)
	}
	all := out.String() + errOut.String()
	if !strings.Contains(all, `"serverCode": "GRAPHQL_VALIDATION_FAILED"`) {
		t.Errorf("envelope lacks the framework serverCode:\n%s", all)
	}
	if !strings.Contains(all, "cac6c05c6") {
		t.Errorf("envelope lacks the actionable message:\n%s", all)
	}
}

// Controls: failures that are NOT exactly this refusal keep their existing
// path and never earn the new sentence.
func TestOrgSigningOlderServerMessageIsNarrow(t *testing.T) {
	cases := []struct {
		name string
		body string
		exit int
		must string // text the existing path already prints ("" = none asserted)
	}{
		{"mixed envelope: missing field beside FORBIDDEN",
			errsResp(valErr("agentUrn", "Row"), `{"message":"Forbidden","extensions":{"code":"FORBIDDEN"}}`), -1, ""},
		{"mixed envelope: both fields beside another unknown field",
			errsResp(valErr("agentUrn", "Row"), valErr("somethingElse", "Row")), exitcode.Usage, "out of sync"},
		{"validation error naming another field",
			"HTTP400 " + errsResp(valErr("orgBootPromptSigningPreview", "Query")), exitcode.Usage, "out of sync"},
		{"unparsed 400 body",
			`HTTP400 GRAPHQL_VALIDATION_FAILED: Cannot query field "agentUrn"`, exitcode.Usage, "out of sync"},
		{"FORBIDDEN", forbiddenResp, exitcode.Forbidden, ""},
		{"internal fault", `HTTP400 {"errors":[{"message":"boom","extensions":{"code":"INTERNAL_SERVER_ERROR"}}],"data":null}`, -1, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fake := fakeAnswering(t, c.body)
			f, out, errOut := testFactoryTTY(t, "")
			code := runSigning(t, f, fake, "org", "signing", "preview", "acme.com")
			all := out.String() + errOut.String()
			if code == 0 {
				t.Fatalf("exit 0, want a refusal:\n%s", all)
			}
			if c.exit >= 0 && code != c.exit {
				t.Errorf("exit %d, want %d:\n%s", code, c.exit, all)
			}
			if c.must != "" && !strings.Contains(all, c.must) {
				t.Errorf("existing message lost (%q):\n%s", c.must, all)
			}
			if strings.Contains(all, "cac6c05c6") {
				t.Errorf("the new sentence must not fire here:\n%s", all)
			}
		})
	}
}
