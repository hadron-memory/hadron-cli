package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

// cli#3014: `secret client issue --mode USE` with the binding pair and the holder
// flags. Fake HTTP only. Values reach the wire byte for byte (cor:acl:140:09: a
// person signs the bytes they typed); the server judges every value and holder.

// rawIssueInput3014 is the issue call's input object exactly as sent.
func rawIssueInput3014(t *testing.T, calls *[]attnCall) map[string]json.RawMessage {
	t.Helper()
	if len(*calls) == 0 || (*calls)[0].Op != "IssueSecretReadClient" {
		t.Fatalf("no issue call was sent: %v", opsOf2631(calls))
	}
	var v struct {
		Input map[string]json.RawMessage `json:"input"`
	}
	if err := json.Unmarshal((*calls)[0].RawVars, &v); err != nil {
		t.Fatal(err)
	}
	return v.Input
}

// A USE issue sends mode, bindingPrefix and bindingHeader unchanged, including
// values a client might be tempted to normalize.
func TestSecretClientIssueUseSendsTheBindingVerbatim3014(t *testing.T) {
	for name, tc := range map[string]struct{ prefix, header string }{
		"plain":           {"https://api.example.com/v1/", "X-Api-Key"},
		"upper-case host": {"https://API.Example.COM/V1/", "x-API-key"},
		"no trailing /":   {"https://api.example.com/v1", "Authorization"},
		"spaces":          {"  https://api.example.com/v1/  ", "  X-Key  "},
	} {
		t.Run(name, func(t *testing.T) {
			srv, calls := attnServer(t, issueResponses2631("OPEN"))
			_, errOut, err := runSecretClient2631(t, srv.URL, "issue", "s1", "--reason", "r", "--mode", "USE",
				"--agent", "ag-1", "--binding-prefix", tc.prefix, "--binding-header", tc.header)
			if err != nil {
				t.Fatalf("%v\n%s", err, errOut)
			}
			in := rawIssueInput3014(t, calls)
			want := func(k string, v string) {
				b, _ := json.Marshal(v)
				if string(in[k]) != string(b) {
					t.Errorf("%s = %s, want %s", k, in[k], b)
				}
			}
			want("mode", "USE")
			want("bindingPrefix", tc.prefix)
			want("bindingHeader", tc.header)
		})
	}
}

// A REVEAL issue sends none of the three, whether --mode REVEAL is given or not.
func TestSecretClientIssueRevealSendsNoUseArguments3014(t *testing.T) {
	for name, args := range map[string][]string{
		"default":       {"issue", "s1", "--reason", "r"},
		"explicit":      {"issue", "s1", "--reason", "r", "--mode", "REVEAL"},
		"lower-case":    {"issue", "s1", "--reason", "r", "--mode", "reveal"},
		"with a worker": {"issue", "s1", "--reason", "r", "--mode", "REVEAL", "--worker", "w1"},
	} {
		t.Run(name, func(t *testing.T) {
			srv, calls := attnServer(t, issueResponses2631("OPEN"))
			if _, errOut, err := runSecretClient2631(t, srv.URL, args...); err != nil {
				t.Fatalf("%v\n%s", err, errOut)
			}
			in := rawIssueInput3014(t, calls)
			for _, k := range []string{"mode", "bindingPrefix", "bindingHeader"} {
				if _, present := in[k]; present {
					t.Errorf("a REVEAL issue must omit %s: %s", k, (*calls)[0].RawVars)
				}
			}
		})
	}
}

// The holder flags: --agent sends AGENT and the id unchanged; --app-holder sends
// APP and omits holderRef (not null, not empty).
func TestSecretClientIssueUseHolders3014(t *testing.T) {
	srv, calls := attnServer(t, issueResponses2631("OPEN"))
	if _, errOut, err := runSecretClient2631(t, srv.URL, "issue", "s1", "--reason", "r", "--mode", "use",
		"--agent", " Ag-1 ", "--binding-prefix", "https://a.example/", "--binding-header", "X-K"); err != nil {
		t.Fatalf("%v\n%s", err, errOut)
	}
	in := rawIssueInput3014(t, calls)
	if string(in["holderKind"]) != `"AGENT"` || string(in["holderRef"]) != `" Ag-1 "` || string(in["mode"]) != `"USE"` {
		t.Errorf("--agent: %s", (*calls)[0].RawVars)
	}

	srv2, calls2 := attnServer(t, issueResponses2631("OPEN"))
	if _, errOut, err := runSecretClient2631(t, srv2.URL, "issue", "s1", "--reason", "r", "--mode", "USE",
		"--app-holder", "--binding-prefix", "https://a.example/", "--binding-header", "X-K"); err != nil {
		t.Fatalf("%v\n%s", err, errOut)
	}
	in2 := rawIssueInput3014(t, calls2)
	if string(in2["holderKind"]) != `"APP"` {
		t.Errorf("--app-holder: %s", (*calls2)[0].RawVars)
	}
	if _, present := in2["holderRef"]; present {
		t.Errorf("--app-holder must omit holderRef: %s", (*calls2)[0].RawVars)
	}
}

// Usage errors (exit 2) send nothing. With neither holder flag a USE issue must
// not fall back to the default USER holder.
func TestSecretClientIssueUseUsageErrorsSendNothing3014(t *testing.T) {
	bind := []string{"--binding-prefix", "https://a.example/", "--binding-header", "X-K"}
	for name, args := range map[string][]string{
		"USE with both holders":          append([]string{"issue", "s1", "--reason", "r", "--mode", "USE", "--agent", "a", "--app-holder"}, bind...),
		"USE with neither holder":        append([]string{"issue", "s1", "--reason", "r", "--mode", "USE"}, bind...),
		"USE with a worker":              append([]string{"issue", "s1", "--reason", "r", "--mode", "USE", "--worker", "w"}, bind...),
		"USE with a worker and an agent": append([]string{"issue", "s1", "--reason", "r", "--mode", "USE", "--worker", "w", "--agent", "a"}, bind...),
		"USE with a blank agent":         append([]string{"issue", "s1", "--reason", "r", "--mode", "USE", "--agent", " "}, bind...),
		"USE with no binding":            {"issue", "s1", "--reason", "r", "--mode", "USE", "--app-holder"},
		"USE with only a prefix":         {"issue", "s1", "--reason", "r", "--mode", "USE", "--app-holder", "--binding-prefix", "https://a.example/"},
		"USE with only a header":         {"issue", "s1", "--reason", "r", "--mode", "USE", "--agent", "a", "--binding-header", "X-K"},
		"blank agent, mode defaulted":    {"issue", "s1", "--reason", "r", "--agent", ""},
		"agent, mode defaulted":          {"issue", "s1", "--reason", "r", "--agent", "a"},
		"agent, REVEAL":                  {"issue", "s1", "--reason", "r", "--mode", "REVEAL", "--agent", "a"},
		"app-holder, mode defaulted":     {"issue", "s1", "--reason", "r", "--app-holder"},
		"app-holder, REVEAL":             {"issue", "s1", "--reason", "r", "--mode", "REVEAL", "--app-holder"},
		"binding prefix, mode defaulted": {"issue", "s1", "--reason", "r", "--binding-prefix", "https://a.example/"},
		"binding header, REVEAL":         {"issue", "s1", "--reason", "r", "--mode", "REVEAL", "--binding-header", "X-K"},
		"an unknown mode":                {"issue", "s1", "--reason", "r", "--mode", "INJECT"},
		"an empty mode":                  {"issue", "s1", "--reason", "r", "--mode", ""},
	} {
		t.Run(name, func(t *testing.T) {
			srv, calls := attnServer(t, issueResponses2631("OPEN"))
			_, _, err := runSecretClient2631(t, srv.URL, args...)
			if exitCodeFor(err) != 2 || len(*calls) != 0 {
				t.Fatalf("exit=%d calls=%v err=%v", exitCodeFor(err), opsOf2631(calls), err)
			}
		})
	}
}

// A USE issue is pending until a person signs, and nothing it prints or its help
// says or suggests the client can be revealed (a USE client never is).
func TestSecretClientIssueUseSaysPendingAndNeverReveal3014(t *testing.T) {
	srv, _ := attnServer(t, issueResponses2631("OPEN"))
	out, errOut, err := runSecretClient2631(t, srv.URL, "issue", "s1", "--reason", "r", "--mode", "USE", "--app-holder",
		"--binding-prefix", "https://a.example/", "--binding-header", "X-K")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errOut, "pending") || !strings.Contains(errOut, "until a person signs") {
		t.Errorf("a USE issue must say it is pending until a person signs: %q", errOut)
	}
	if all := strings.ToLower(out + errOut); strings.Contains(all, "reveal") {
		t.Errorf("a USE issue must not mention revealing: %q %q", out, errOut)
	}

	help, _, herr := runSecretClient2631(t, srv.URL, "issue", "--help")
	if herr != nil {
		t.Fatal(herr)
	}
	i := strings.Index(help, "USE mode")
	if i < 0 {
		t.Fatalf("help must describe USE mode:\n%s", help)
	}
	para := help[i:]
	if j := strings.Index(para, "\n\n"); j > 0 {
		para = para[:j]
	}
	if !strings.Contains(para, "pending") || strings.Contains(strings.ToLower(para), "reveal") {
		t.Errorf("the USE help paragraph must say pending and never suggest a reveal: %q", para)
	}
}

// Control, green today: a USE client is listed like any client (ten fields, no
// mode or binding column: the issue's gate resolved the display as unchanged).
func TestSecretClientListShowsAUseClientAsAnyClient3014(t *testing.T) {
	row := `{"clientId":null,"clientPrefix":"c1","grantId":"g1","secretId":"s1","secretName":"tok","selfIssued":false,"endReason":null,"createdAt":"2026-11-20T00:00:00Z","destroyOnEnd":false}`
	srv, _ := attnServer(t, map[string]string{listOp2758: `{"data":{"mySecretReadClients":[` + row + `]}}`})
	out, _, err := runSecretClient2631(t, srv.URL, "ls", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var rows []map[string]any
	if e := json.Unmarshal([]byte(out), &rows); e != nil || len(rows) != 1 || len(rows[0]) != 10 {
		t.Fatalf("a list row keeps its ten fields: %v\n%s", e, out)
	}
}

// Blind P3: a blank --agent without --mode USE gets the same diagnosis as a
// non-blank one (it is a USE flag), not a different "needs a value" message.
func TestSecretClientIssueBlankAgentWithoutUseSaysUseFlag3014(t *testing.T) {
	srv, _ := attnServer(t, issueResponses2631("OPEN"))
	_, _, err := runSecretClient2631(t, srv.URL, "issue", "s1", "--reason", "r", "--agent", "")
	if err == nil || !strings.Contains(err.Error(), "USE flag") {
		t.Fatalf("want the USE-flag diagnosis, got %v", err)
	}
}

// Blind P3: --mode is trimmed and case-folded (documented), so " use " is USE.
func TestSecretClientIssueModeIsTrimmedAndCaseFolded3014(t *testing.T) {
	srv, calls := attnServer(t, issueResponses2631("OPEN"))
	_, errOut, err := runSecretClient2631(t, srv.URL, "issue", "s1", "--reason", "r", "--mode", " use ", "--app-holder",
		"--binding-prefix", "https://a.example/", "--binding-header", "X-K")
	if err != nil {
		t.Fatalf("%v\n%s", err, errOut)
	}
	if got := string(rawIssueInput3014(t, calls)["mode"]); got != `"USE"` {
		t.Errorf("mode = %s", got)
	}
}

// Blind P3: a blank binding value is deliberately sent (the server judges every
// value; the person signs the bytes typed), not dropped.
func TestSecretClientIssueUseSendsABlankBindingValue3014(t *testing.T) {
	srv, calls := attnServer(t, issueResponses2631("OPEN"))
	_, errOut, err := runSecretClient2631(t, srv.URL, "issue", "s1", "--reason", "r", "--mode", "USE", "--app-holder",
		"--binding-prefix", "", "--binding-header", " ")
	if err != nil {
		t.Fatalf("%v\n%s", err, errOut)
	}
	in := rawIssueInput3014(t, calls)
	if string(in["bindingPrefix"]) != `""` || string(in["bindingHeader"]) != `" "` {
		t.Errorf("blank bindings must reach the wire: %s %s", in["bindingPrefix"], in["bindingHeader"])
	}
}

// Blind P3: USE composes with --wait and --auto-delete.
func TestSecretClientIssueUseComposesWithWaitAndAutoDelete3014(t *testing.T) {
	srv, calls := attnServer(t, issueResponses2631("COMPLETED"))
	out, errOut, err := runSecretClient2631(t, srv.URL, "issue", "s1", "--reason", "r", "--mode", "USE", "--app-holder",
		"--binding-prefix", "https://a.example/", "--binding-header", "X-K", "--max-reads", "1", "--auto-delete", "--wait", "--json")
	if err != nil {
		t.Fatalf("%v\n%s", err, errOut)
	}
	if !strings.Contains(errOut, ceremonyURL2631) || !strings.Contains(errOut, "note: --auto-delete was sent") {
		t.Errorf("stderr must carry the ceremony URL and the auto-delete note: %q", errOut)
	}
	if in := rawIssueInput3014(t, calls); string(in["mode"]) != `"USE"` || string(in["autoDelete"]) != "true" {
		t.Errorf("input: %v", in)
	}
	if !strings.Contains(out, "COMPLETED") {
		t.Errorf("the record must print the end state: %s", out)
	}
}
