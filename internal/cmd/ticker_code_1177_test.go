package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/output"
)

// cli#1177 (T-C1, server#2733 / hadron-server 43adbe50): tickers show their
// stored tick code and the register entry's loc as the server returns it;
// `ticker create` surfaces the ticker.code.unmapped warning.

// withCode adds tickCode and registerTaskLoc to the ticker fixture; loc "" is
// a null loc.
func withCode(code, loc string) string {
	l := "null"
	if loc != "" {
		l = `"` + loc + `"`
	}
	return strings.TrimSuffix(tickerFixture, "}") + `,"tickCode":"` + code + `","registerTaskLoc":` + l + `}`
}

// preChangeTickerKeys is the key set before #1177: none may be renamed or
// dropped (review:stable-json-dto).
var preChangeTickerKeys = []string{"id", "urn", "name", "slug", "appId", "cadenceSeconds", "anchorAt", "quietHours", "readableChannels", "relevance", "reasons", "enabled", "owner", "nextTickAt", "lastTickAt", "createdAt", "updatedAt"}

func tickerResponses(fixture string) map[string]string {
	return map[string]string{
		"GetTicker":         `{"data":{"ticker":` + fixture + `}}`,
		"ListTickersPage":   `{"data":{"tickersPage":{"total":1,"hasMore":false,"items":[` + fixture + `]}}}`,
		"CreateTicker":      `{"data":{"createTicker":` + fixture + `}}`,
		"UpdateTicker":      `{"data":{"updateTicker":` + fixture + `}}`,
		"TickerSubscribers": `{"data":{"ticker":{"subscribers":{"items":[],"hasNextPage":false,"endCursor":null}}}}`,
		"TeamAppIdentity":   `{"data":{"app":{"id":"app1","urn":"hrn:app:acme.com:ops","name":"Ops"}}}`,
	}
}

var tickerVerbs = map[string][]string{
	"get":    {"ticker", "get", tickerRef},
	"list":   {"ticker", "list", "--app", "acme.com:ops"},
	"create": {"ticker", "create", "--app", "acme.com:ops", "--name", "daily", "--cadence-seconds", "600"},
	"update": {"ticker", "update", tickerRef, "--name", "daily"},
}

func runTicker(t *testing.T, responses map[string]string, args ...string) (string, string, error) {
	t.Helper()
	gql := fakeGraphQL(t, responses)
	f, out := testFactory(t)
	errOut := &strings.Builder{}
	f.IOStreams.ErrOut = errOut
	r := NewRootCmd(f)
	r.SetArgs(append(args, "--server", gql.URL))
	err := r.Execute()
	return out.String(), errOut.String(), err
}

func TestC1177JSONCarriesCodeAndLocAdditively(t *testing.T) {
	for verb, args := range tickerVerbs {
		for _, loc := range []string{"", "tasks:on-base-tick"} {
			t.Run(verb+"/"+loc, func(t *testing.T) {
				out, _, err := runTicker(t, tickerResponses(withCode("tick.daily", loc)), append(append([]string{}, args...), "--json")...)
				if err != nil {
					t.Fatal(err)
				}
				var obj map[string]json.RawMessage
				if verb == "list" {
					var arr []map[string]json.RawMessage
					if err := json.Unmarshal([]byte(out), &arr); err != nil || len(arr) != 1 {
						t.Fatalf("%v: %s", err, out)
					}
					obj = arr[0]
				} else if err := json.Unmarshal([]byte(out), &obj); err != nil {
					t.Fatal(err)
				}
				if string(obj["tickCode"]) != `"tick.daily"` {
					t.Fatalf("tickCode %s in %s", obj["tickCode"], out)
				}
				want := "null"
				if loc != "" {
					want = `"` + loc + `"`
				}
				if string(obj["registerTaskLoc"]) != want {
					t.Fatalf("registerTaskLoc %s, want %s in %s", obj["registerTaskLoc"], want, out)
				}
				for _, k := range preChangeTickerKeys {
					if _, ok := obj[k]; !ok {
						t.Fatalf("key %q dropped: %s", k, out)
					}
				}
			})
		}
	}
}

func TestC1177TextShowsCodeAndLocWithoutClaimingUnmapped(t *testing.T) {
	out, _, err := runTicker(t, tickerResponses(withCode("tick.daily", "tasks:on-base-tick")), tickerVerbs["get"]...)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "  code tick.daily · register task loc tasks:on-base-tick\n") {
		t.Fatalf("no code line:\n%s", out)
	}
	if !strings.Contains(out, "owner ") || strings.Index(out, "code tick.daily") < strings.Index(out, "owner ") {
		t.Fatalf("the code line follows the owner/anchor line:\n%s", out)
	}
	out, _, err = runTicker(t, tickerResponses(withCode("tick.daily", "")), tickerVerbs["get"]...)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "  code tick.daily · no register entry you can read\n") {
		t.Fatalf("null loc line:\n%s", out)
	}
	for _, banned := range []string{"unmapped", "no task mapped"} {
		if strings.Contains(strings.ToLower(out), banned) {
			t.Fatalf("a null loc must not say %q (it also means \"can't read\"):\n%s", banned, out)
		}
	}
}

func TestC1177ServerStringsAreSafeCells(t *testing.T) {
	out, _, err := runTicker(t, tickerResponses(withCode(`tick.x\u001b[31mred`, `tasks:\u001b]0;pwn\u0007y`)), tickerVerbs["get"]...)
	if err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(out, "\x1b\x07") {
		t.Fatalf("control characters reached the terminal: %q", out)
	}
}

const unmappedWarning = `[{"path":"createTicker","code":"ticker.code.unmapped","text":"No register entry maps tick.daily."}`

func withWarnings(op, fixture, warnings string) map[string]string {
	r := tickerResponses(fixture)
	r[op] = strings.TrimSuffix(r[op], "}") + `,"extensions":{"warnings":` + warnings + `}}`
	return r
}

func TestC1177CreateWarningIsOnStderrAfterAnUnchangedReceipt(t *testing.T) {
	base, _, err := runTicker(t, tickerResponses(withCode("tick.daily", "")), tickerVerbs["create"]...)
	if err != nil {
		t.Fatal(err)
	}
	out, errOut, err := runTicker(t, withWarnings("CreateTicker", withCode("tick.daily", ""), unmappedWarning+`]`), tickerVerbs["create"]...)
	if err != nil {
		t.Fatalf("a warning changed the exit: %v", err)
	}
	if out != base {
		t.Fatalf("stdout changed:\n got %q\nwant %q", out, base)
	}
	if errOut != "warning: [ticker.code.unmapped] No register entry maps tick.daily.\n" {
		t.Fatalf("stderr %q", errOut)
	}
	// A mapped register adds the task.
	mapped := unmappedWarning[:len(unmappedWarning)-1] + `,"register":{"state":"mapped","taskLoc":"tasks:t","taskUrn":"hrn:node:a:b:tasks:t"}}]`
	_, errOut, err = runTicker(t, withWarnings("CreateTicker", withCode("tick.daily", ""), mapped), tickerVerbs["create"]...)
	if err != nil || !strings.HasSuffix(errOut, " → hrn:node:a:b:tasks:t\n") {
		t.Fatalf("%v %q", err, errOut)
	}
}

func TestC1177WarningPrintsAfterTheReceiptOnOneBuffer(t *testing.T) {
	gql := fakeGraphQL(t, withWarnings("CreateTicker", withCode("tick.daily", ""), unmappedWarning+`]`))
	f, out := testFactory(t)
	f.IOStreams.Out = output.Tracked(out)
	f.IOStreams.ErrOut = out
	r := NewRootCmd(f)
	r.SetArgs(append(append([]string{}, tickerVerbs["create"]...), "--server", gql.URL))
	if err := r.Execute(); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	if i, j := strings.Index(s, "daily"), strings.Index(s, "warning:"); i < 0 || j < 0 || j < i {
		t.Fatalf("the warning must follow the receipt:\n%s", s)
	}
}

func TestC1177CreateWarningsJSONAlwaysPresent(t *testing.T) {
	out, errOut, err := runTicker(t, withWarnings("CreateTicker", withCode("tick.daily", ""), unmappedWarning+`]`), append(append([]string{}, tickerVerbs["create"]...), "--json")...)
	if err != nil || errOut != "" {
		t.Fatalf("%v stderr %q", err, errOut)
	}
	var v struct {
		Warnings []struct {
			Code     string          `json:"code"`
			Text     string          `json:"text"`
			Register json.RawMessage `json:"register"`
		} `json:"warnings"`
	}
	if err := json.Unmarshal([]byte(out), &v); err != nil || len(v.Warnings) != 1 || v.Warnings[0].Code != "ticker.code.unmapped" || string(v.Warnings[0].Register) != "null" {
		t.Fatalf("%v: %s", err, out)
	}
	out, _, err = runTicker(t, tickerResponses(withCode("tick.daily", "")), append(append([]string{}, tickerVerbs["create"]...), "--json")...)
	if err != nil || !strings.Contains(out, `"warnings": []`) {
		t.Fatalf("%v: %s", err, out)
	}
}

func TestC1177OnlyCreateTickersWarningsAreShown(t *testing.T) {
	other := `[{"path":"someOtherMutation","code":"x.y","text":"not ours"}]`
	_, errOut, err := runTicker(t, withWarnings("CreateTicker", withCode("tick.daily", ""), other), tickerVerbs["create"]...)
	if err != nil || errOut != "" {
		t.Fatalf("%v stderr %q", err, errOut)
	}
	for _, verb := range []string{"update", "get", "list"} {
		op := map[string]string{"update": "UpdateTicker", "get": "GetTicker", "list": "ListTickersPage"}[verb]
		out, errOut, err := runTicker(t, withWarnings(op, withCode("tick.daily", ""), unmappedWarning+`]`), append(append([]string{}, tickerVerbs[verb]...), "--json")...)
		if err != nil || errOut != "" || strings.Contains(out, `"warnings"`) {
			t.Fatalf("%s: %v stderr %q out %s", verb, err, errOut, out)
		}
	}
}

func TestC1177RefusedCreateStaysExit5WithNoWarning(t *testing.T) {
	for _, code := range []string{"TICKER_NAME_TAKEN", "TICKER_LIMIT_REACHED"} {
		r := tickerResponses(withCode("tick.daily", ""))
		r["CreateTicker"] = `{"errors":[{"message":"` + code + `: no","extensions":{"code":"` + code + `"}}],"extensions":{"warnings":` + unmappedWarning + `]}}`
		_, errOut, err := runTicker(t, r, tickerVerbs["create"]...)
		if err == nil || exitCodeFor(err) != 5 || strings.Contains(errOut, "ticker.code.unmapped") {
			t.Fatalf("%s: %v stderr %q", code, err, errOut)
		}
	}
}
