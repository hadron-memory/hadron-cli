package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// Draft spec corpora and minting (hadron-server#1447, cli#777).

const notDraftErr = `{"errors":[{"message":"Only a draft spec corpus can reserve a citation: this memory is minted.","extensions":{"code":"SPEC_CORPUS_NOT_DRAFT"}}]}`

// mintReport is a mintSpecCorpus response. blockers is raw JSON (an array).
func mintReport(dryRun, minted bool, blockers string) string {
	b := func(v bool) string {
		if v {
			return "true"
		}
		return "false"
	}
	return `{"data":{"mintSpecCorpus":{"memoryId":"mem1","dryRun":` + b(dryRun) + `,"minted":` + b(minted) + `,` +
		`"staleAbstractsBlock":false,"blockers":` + blockers + `,` +
		`"staleAbstracts":[{"kind":"STALE_ABSTRACT","rule":null,"loc":"pas:010:01","targetLoc":null,"message":"abstract written for an earlier body"}],` +
		`"openQuestions":[{"decider":"Duygu","questions":[{"loc":"pas:030:01","question":"What does Log out do?"}]},` +
		`{"decider":null,"questions":[{"loc":"pas:030:01","question":"Which help centre?"}]}]}}}`
}

const placeholderBlocker = `[{"kind":"PLACEHOLDER","rule":null,"loc":"pas:010:04","targetLoc":null,"message":"reserved, not written"}]`

// mintServer answers MintSpecCorpus per call and records each call's dryRun,
// so a test can prove the real (dryRun:false) mint was or was not sent — a
// static map keeps only the LAST call's variables, which cannot tell.
func mintServer(t *testing.T, check, real string) (url string, calls func() []bool) {
	t.Helper()
	var mu sync.Mutex
	var dry []bool
	srv := newVarServer(t, func(op string, vars json.RawMessage) string {
		switch op {
		case "Memories":
			return memListJSON
		case "MintMemoryNodes":
			resp, _ := unstubbedDefault(op)
			return resp
		case "MintSpecCorpus":
			var v struct {
				DryRun bool `json:"dryRun"`
			}
			_ = json.Unmarshal(vars, &v)
			mu.Lock()
			dry = append(dry, v.DryRun)
			mu.Unlock()
			if v.DryRun {
				return check
			}
			return real
		}
		t.Errorf("unexpected operation %q", op)
		return `{"errors":[{"message":"unexpected operation"}]}`
	})
	return srv.URL, func() []bool {
		mu.Lock()
		defer mu.Unlock()
		return append([]bool(nil), dry...)
	}
}

func TestSpecMintModernUsesPerNodeReportWithoutLegacyMutation(t *testing.T) {
	var calls []bool
	srv := newVarServer(t, func(op string, raw json.RawMessage) string {
		switch op {
		case "Memories":
			return memListJSON
		case "MintMemoryNodes":
			var vars struct {
				DryRun bool `json:"dryRun"`
			}
			_ = json.Unmarshal(raw, &vars)
			calls = append(calls, vars.DryRun)
			if vars.DryRun {
				return `{"data":{"mintMemoryNodes":{"memoryId":"mem1","dryRun":true,"minted":false,"mintedCount":0,"mintLocs":["pas:010:01"],"blockers":[],"staleAbstracts":[],"staleAbstractsBlock":false,"openQuestions":[]}}}`
			}
			return `{"data":{"mintMemoryNodes":{"memoryId":"mem1","dryRun":false,"minted":true,"mintedCount":1,"mintLocs":["pas:010:01"],"blockers":[],"staleAbstracts":[],"staleAbstractsBlock":false,"openQuestions":[]}}}`
		default:
			t.Errorf("unexpected operation %q", op)
			return `{}`
		}
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "mint", "-m", "mem1", "--yes", "--json", "--server", srv.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 2 || !calls[0] || calls[1] {
		t.Errorf("mint calls = %v", calls)
	}
	var got struct {
		MintedCount *int     `json:"mintedCount"`
		MintLocs    []string `json:"mintLocs"`
	}
	if err := json.Unmarshal([]byte(out.String()), &got); err != nil {
		t.Fatal(err)
	}
	if got.MintedCount == nil || *got.MintedCount != 1 || len(got.MintLocs) != 1 || got.MintLocs[0] != "pas:010:01" {
		t.Errorf("report = %+v", got)
	}
}

// newVarServer is a fake GraphQL server whose answer depends on the request's
// variables as well as its operation — what the mint flow needs, since the
// check and the real mint are the SAME operation told apart by dryRun.
func newVarServer(t *testing.T, respond func(op string, vars json.RawMessage) string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			OperationName string          `json:"operationName"`
			Variables     json.RawMessage `json:"variables"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(respond(body.OperationName, body.Variables)))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestSpecReserveSendsCitationAndOmitsUnsetName(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"Memories": memListJSON,
		"ReserveSpecCitation": `{"data":{"reserveSpecCitation":{"id":"n1","loc":"pas:010:04","name":"pas:010:04",` +
			`"role":"spec","isPlaceholder":true}}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "reserve", "pas:010:04", "-m", "mem1", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("reserve: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["ReserveSpecCitation"], &vars)
	if vars["memoryRef"] != "mem1" || vars["loc"] != "pas:010:04" {
		t.Errorf("unexpected variables: %v", vars)
	}
	if _, sent := vars["name"]; sent {
		t.Errorf("an unset --name must be omitted, not sent: %v", vars)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(out.String()), &got); err != nil {
		t.Fatalf("--json: %v (%q)", err, out.String())
	}
	if got["placeholder"] != true || got["loc"] != "pas:010:04" || got["role"] != "spec" {
		t.Errorf("unexpected DTO: %v", got)
	}
}

func TestSpecReserveBlankNameRefusedOffline(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	// No server: a blank --name must be refused before any request.
	root.SetArgs([]string{"spec", "reserve", "pas:010:04", "-m", "mem1", "--name", "  ", "--server", "http://127.0.0.1:1"})
	if got := exitCodeFor(root.Execute()); got != exitcode.Usage {
		t.Errorf("a blank --name should exit 2, got %d", got)
	}
}

// A minted corpus refuses every draft-only command with SPEC_CORPUS_NOT_DRAFT;
// the corpus's state refuses, and no retry changes a one-way mint: exit 5.
func TestSpecDraftCommandsOnMintedCorpusExit5(t *testing.T) {
	for _, c := range []struct {
		op   string
		args []string
	}{
		{"ReserveSpecCitation", []string{"spec", "reserve", "pas:010:04"}},
		{"SpecBacklinks", []string{"spec", "backlinks", "pas:010:04"}},
		{"SpecUnresolvedReferences", []string{"spec", "unresolved"}},
		{"RenumberSpec", []string{"spec", "renumber", "pas:010:04", "pas:010:05"}},
		{"MintSpecCorpus", []string{"spec", "mint", "--dry-run"}},
	} {
		gql := fakeGraphQL(t, map[string]string{"Memories": memListJSON, c.op: notDraftErr})
		f, _ := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs(append(c.args, "-m", "mem1", "--server", gql.URL))
		if got := exitCodeFor(root.Execute()); got != exitcode.Conflict {
			t.Errorf("%s on a minted corpus should exit 5, got %d", c.op, got)
		}
	}
}

func TestSpecBacklinksEmptyIsArrayNotNull(t *testing.T) {
	gql := fakeGraphQL(t, map[string]string{
		"Memories":      memListJSON,
		"SpecBacklinks": `{"data":{"specBacklinks":[]}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "backlinks", "pas:010:01", "-m", "mem1", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("backlinks: %v", err)
	}
	if !strings.Contains(out.String(), `"references": []`) {
		t.Errorf("an empty result must render references as [], got %s", out.String())
	}
}

func TestSpecBacklinksAndUnresolvedCarryTheReference(t *testing.T) {
	ref := `{"kind":"URN","field":"content","sourceLoc":"pas:010","sourceNodeId":"n9","targetLoc":"pas:010:01",` +
		`"text":"hrn:node:micromentor.org:specs-draft:pas:010:01"`
	gql := fakeGraphQL(t, map[string]string{
		"Memories":                 memListJSON,
		"SpecBacklinks":            `{"data":{"specBacklinks":[` + ref + `}]}}`,
		"SpecUnresolvedReferences": `{"data":{"specUnresolvedReferences":[` + ref + `,"reason":"PLACEHOLDER"}]}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "backlinks", "pas:010:01", "-m", "mem1", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("backlinks: %v", err)
	}
	var bl struct {
		References []map[string]any `json:"references"`
	}
	_ = json.Unmarshal([]byte(out.String()), &bl)
	if len(bl.References) != 1 || bl.References[0]["sourceLoc"] != "pas:010" || bl.References[0]["kind"] != "URN" {
		t.Errorf("unexpected backlinks: %s", out.String())
	}
	if _, has := bl.References[0]["reason"]; has {
		t.Errorf("backlinks carry no reason: %s", out.String())
	}

	f, out = testFactory(t)
	root = NewRootCmd(f)
	root.SetArgs([]string{"spec", "unresolved", "-m", "mem1", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("unresolved exits 0 even with references listed: %v", err)
	}
	var un struct {
		References []map[string]any `json:"references"`
	}
	_ = json.Unmarshal([]byte(out.String()), &un)
	if len(un.References) != 1 || un.References[0]["reason"] != "PLACEHOLDER" {
		t.Errorf("unexpected unresolved: %s", out.String())
	}
}

func TestSpecRenumberSameCitationRefusedOffline(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "renumber", "pas:010:04", " pas:010:04 ", "-m", "mem1", "--server", "http://127.0.0.1:1"})
	if got := exitCodeFor(root.Execute()); got != exitcode.Usage {
		t.Errorf("renumbering a citation onto itself should exit 2, got %d", got)
	}
}

// A FAILED rewrite leaves the move standing and a node still naming the old
// citation — a partial write, reported in full and exiting non-zero.
func TestSpecRenumberFailedRewriteReportsAndExits1(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"Memories": memListJSON,
		"RenumberSpec": `{"data":{"renumberSpec":{"dryRun":false,` +
			`"moved":[{"fromLoc":"pas:010:04","toLoc":"pas:010:02"}],` +
			`"rewrites":[{"nodeId":"n1","loc":"pas:010","status":"REWRITTEN","fields":["content"],"count":2,"error":null},` +
			`{"nodeId":"n2","loc":"pas:030:01","status":"FAILED","fields":["content"],"count":0,"error":"kept conflicting"}],` +
			`"textCitations":[{"sourceNodeId":"n1","sourceLoc":"pas:010","field":"content","citation":"pas:010:04","excerpt":"[… [pas:010:04]](…)"}]}}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "renumber", "pas:010:04", "pas:010:02", "-m", "mem1", "--json", "--server", gql.URL})
	if got := exitCodeFor(root.Execute()); got != exitcode.Error {
		t.Errorf("a FAILED rewrite should exit 1, got %d", got)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["RenumberSpec"], &vars)
	if vars["dryRun"] != false || vars["fromLoc"] != "pas:010:04" || vars["toLoc"] != "pas:010:02" {
		t.Errorf("unexpected variables: %v", vars)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(out.String()), &got); err != nil {
		t.Fatalf("the report must still be written: %v (%q)", err, out.String())
	}
	if got["failed"] != float64(1) || len(got["textCitations"].([]any)) != 1 || len(got["rewrites"].([]any)) != 2 {
		t.Errorf("unexpected report: %v", got)
	}
}

func TestSpecRenumberDryRunSendsDryRun(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"Memories": memListJSON,
		"RenumberSpec": `{"data":{"renumberSpec":{"dryRun":true,"moved":[{"fromLoc":"a:1","toLoc":"a:2"}],` +
			`"rewrites":[{"nodeId":"n1","loc":"a","status":"PLANNED","fields":["content"],"count":1,"error":null}],"textCitations":[]}}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "renumber", "a:1", "a:2", "-m", "mem1", "--dry-run", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("dry run: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["RenumberSpec"], &vars)
	if vars["dryRun"] != true {
		t.Errorf("--dry-run must send dryRun:true, got %v", vars)
	}
	if !strings.Contains(out.String(), "dry run, nothing written") {
		t.Errorf("the text output must say nothing was written: %q", out.String())
	}
}

// A blocked corpus is never offered a mint: the check reports it, the real
// mint is never sent, and the exit is 5 — with or without --dry-run/--yes.
func TestSpecMintBlockedNeverSendsTheRealMint(t *testing.T) {
	for _, extra := range [][]string{{"--dry-run"}, {"--yes"}} {
		url, calls := mintServer(t, mintReport(true, false, placeholderBlocker), mintReport(false, true, `[]`))
		f, out := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs(append([]string{"spec", "mint", "-m", "mem1", "--json", "--server", url}, extra...))
		if got := exitCodeFor(root.Execute()); got != exitcode.Conflict {
			t.Errorf("%v: a blocked corpus should exit 5, got %d", extra, got)
		}
		if c := calls(); len(c) != 1 || !c[0] {
			t.Errorf("%v: only the dry-run check may be sent, got dryRun calls %v", extra, c)
		}
		var got map[string]any
		if err := json.Unmarshal([]byte(out.String()), &got); err != nil {
			t.Fatalf("%v: the report must be written: %v (%q)", extra, err, out.String())
		}
		if got["blocked"] != true || got["minted"] != false || len(got["blockers"].([]any)) != 1 {
			t.Errorf("%v: unexpected report: %v", extra, got)
		}
	}
}

func TestSpecMintDryRunCleanExits0AndMintsNothing(t *testing.T) {
	url, calls := mintServer(t, mintReport(true, false, `[]`), mintReport(false, true, `[]`))
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "mint", "-m", "mem1", "--dry-run", "--server", url})
	if err := root.Execute(); err != nil {
		t.Fatalf("a clean dry run exits 0: %v", err)
	}
	if c := calls(); len(c) != 1 || !c[0] {
		t.Errorf("--dry-run must send only the check, got %v", c)
	}
	s := out.String()
	for _, want := range []string{"mintable — run without --dry-run", "Duygu decides:", "unassigned decides:", "stale abstracts: 1 (reported only)"} {
		if !strings.Contains(s, want) {
			t.Errorf("report missing %q:\n%s", want, s)
		}
	}
}

// Minting is irreversible, so a non-interactive caller must pass --yes.
func TestSpecMintNonInteractiveWithoutYesRefused(t *testing.T) {
	url, calls := mintServer(t, mintReport(true, false, `[]`), mintReport(false, true, `[]`))
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "mint", "-m", "mem1", "--server", url})
	if got := exitCodeFor(root.Execute()); got != exitcode.Usage {
		t.Errorf("mint without --yes non-interactively should exit 2, got %d", got)
	}
	if c := calls(); len(c) != 1 || !c[0] {
		t.Errorf("only the check may be sent, got %v", c)
	}
}

func TestSpecMintWithYesChecksThenMints(t *testing.T) {
	url, calls := mintServer(t, mintReport(true, false, `[]`), mintReport(false, true, `[]`))
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "mint", "-m", "mem1", "--yes", "--json", "--server", url})
	if err := root.Execute(); err != nil {
		t.Fatalf("mint --yes: %v", err)
	}
	if c := calls(); len(c) != 2 || !c[0] || c[1] {
		t.Errorf("want the check then the real mint (dryRun true, false), got %v", c)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(out.String()), &got); err != nil {
		t.Fatalf("--json must be ONE document: %v (%q)", err, out.String())
	}
	if got["minted"] != true || got["dryRun"] != false {
		t.Errorf("unexpected result: %v", got)
	}
}

func TestSpecMintTTYDeclineMintsNothing(t *testing.T) {
	url, calls := mintServer(t, mintReport(true, false, `[]`), mintReport(false, true, `[]`))
	f, _, errOut := testFactoryTTY(t, "n\n")
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "mint", "-m", "mem1", "--server", url})
	if got := exitCodeFor(root.Execute()); got != exitcode.Cancelled {
		t.Errorf("declining should exit 6, got %d", got)
	}
	if c := calls(); len(c) != 1 {
		t.Errorf("a declined mint must send only the check, got %v", c)
	}
	if !strings.Contains(errOut.String(), "one-way") {
		t.Errorf("the legacy prompt must state its consequence: %q", errOut.String())
	}
}

// The race between the check and the mint: the server refuses, and the exit
// matches the CLI's own blocked exit so the contract doesn't depend on who
// refused.
func TestSpecMintServerRefusalExits5(t *testing.T) {
	for _, code := range []string{"SPEC_CORPUS_MINT_BLOCKED", "SPEC_CORPUS_BUSY"} {
		url, _ := mintServer(t, mintReport(true, false, `[]`),
			`{"errors":[{"message":"refused","extensions":{"code":"`+code+`"}}]}`)
		f, _ := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs([]string{"spec", "mint", "-m", "mem1", "--yes", "--server", url})
		if got := exitCodeFor(root.Execute()); got != exitcode.Conflict {
			t.Errorf("%s should exit 5, got %d", code, got)
		}
	}
}

func TestSpecDescribeShowsCorpusState(t *testing.T) {
	gql := fakeGraphQL(t, map[string]string{
		"Memories":  memListJSON,
		"GetMemory": memGetJSON(`null`),
		"FindNodes": `{"data":{"nodes":[` + specNodeList("cli", `["spec"]`) + `]}}`,
		"SpecCorpusState": `{"data":{"memory":{"id":"mem1","urn":"hadronmemory.com:platform-specs",` +
			`"corpusState":"DRAFT","corpusMintedAt":null}}}`,
		"SpecPlaceholderScan": placeholderScanJSON(map[string]bool{"cli": false}),
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "describe", "-m", "mem1", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("describe: %v", err)
	}
	if !strings.Contains(out.String(), `"corpusState": "DRAFT"`) {
		t.Errorf("describe must report the corpus state: %s", out.String())
	}
}

func TestSpecDescribeSeparatesDraftPlaceholders(t *testing.T) {
	gql := fakeGraphQL(t, map[string]string{
		"Memories":        memListJSON,
		"GetMemory":       memGetJSON(`null`),
		"FindNodes":       `{"data":{"nodes":[` + specNodeList("msg:010:01", `["spec"]`) + `,` + specNodeList("msg:010:02", `["spec"]`) + `,` + specNodeList("msg:010:03", `["spec"]`) + `]}}`,
		"SpecCorpusState": draftStateJSON,
		"SpecPlaceholderScan": placeholderScanJSON(map[string]bool{
			"msg:010:02": true, "msg:010:03": false, "msg:010:01": true,
		}),
	})
	for _, jsonOutput := range []bool{true, false} {
		f, out := testFactory(t)
		root := NewRootCmd(f)
		args := []string{"spec", "describe", "-m", specMem, "--server", gql.URL}
		if jsonOutput {
			args = append(args, "--json")
		}
		root.SetArgs(args)
		if err := root.Execute(); err != nil {
			t.Fatalf("describe: %v", err)
		}
		if jsonOutput {
			var got struct {
				Specs            int      `json:"specs"`
				PlaceholderCount *int     `json:"placeholderCount"`
				Placeholders     []string `json:"placeholders"`
			}
			if err := json.Unmarshal([]byte(out.String()), &got); err != nil {
				t.Fatal(err)
			}
			if got.Specs != 3 || got.PlaceholderCount == nil || *got.PlaceholderCount != 2 || strings.Join(got.Placeholders, ",") != "msg:010:01,msg:010:02" {
				t.Errorf("describe must separate and sort placeholders: %+v", got)
			}
		} else if !strings.Contains(out.String(), "placeholders: 2 (msg:010:01, msg:010:02)") {
			t.Errorf("human describe must list placeholders: %s", out.String())
		}
	}
}

func TestSpecDescribePlaceholderAvailability(t *testing.T) {
	for _, tc := range []struct {
		name, state, scan string
		wantKnown         bool
	}{
		{"minted", mintedStateJSON, "", true},
		{"older placeholder slice", draftStateJSON,
			`{"errors":[{"message":"Cannot query field \"isPlaceholder\" on type \"Node\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			responses := map[string]string{
				"Memories": memListJSON, "GetMemory": memGetJSON(`null`),
				"FindNodes":       `{"data":{"nodes":[` + specNodeList("msg:010:02", `["spec"]`) + `]}}`,
				"SpecCorpusState": tc.state,
			}
			if tc.scan != "" {
				responses["SpecPlaceholderScan"] = tc.scan
			}
			gql := fakeGraphQL(t, responses)
			f, out := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs([]string{"spec", "describe", "-m", specMem, "--json", "--server", gql.URL})
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
			var got map[string]any
			if err := json.Unmarshal([]byte(out.String()), &got); err != nil {
				t.Fatal(err)
			}
			_, known := got["placeholderCount"]
			if known != tc.wantKnown {
				t.Errorf("placeholder availability = %v, want %v: %s", known, tc.wantKnown, out.String())
			}
			if tc.wantKnown && (got["placeholderCount"] != float64(0) || len(got["placeholders"].([]any)) != 0) {
				t.Errorf("minted inventory must report known empty placeholders: %s", out.String())
			}
			if tc.wantKnown && !strings.Contains(out.String(), `"placeholders": []`) {
				t.Errorf("known empty placeholder list must be [], not null: %s", out.String())
			}
		})
	}
}

// A server that predates draft corpora can't say; the inventory is still
// reported, and the state is left out rather than guessed.
func TestSpecDescribeOnOlderServerOmitsState(t *testing.T) {
	gql := fakeGraphQL(t, map[string]string{
		"Memories":  memListJSON,
		"GetMemory": memGetJSON(`null`),
		"FindNodes": `{"data":{"nodes":[` + specNodeList("cli", `["spec"]`) + `]}}`,
		// SpecCorpusState unstubbed: the default answers as an older server.
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "describe", "-m", "mem1", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("describe must still succeed on an older server: %v", err)
	}
	if !strings.Contains(out.String(), `"corpusState": null`) {
		t.Errorf("an unknown state must be a present null, not guessed or omitted: %s", out.String())
	}
}

func TestMemorySetDraftCorpus(t *testing.T) {
	created := `{"data":{"createMemory":{"id":"m9","urn":"acme.com:product-specs","name":"Product specs",` +
		`"shortDescription":null,"class":"knowledge","visibility":"ORGANIZATION","organizationId":"o1",` +
		`"isEncrypted":false,"maxRevCount":10,"updatedAt":"2026-09-29T00:00:00Z","corpusState":"DRAFT"}}}`

	gql, captured := captureGraphQL(t, map[string]string{
		"CreateMemoryDraft": created,
		"SpecCorpusState": `{"data":{"memory":{"id":"m9","urn":"acme.com:product-specs",` +
			`"corpusState":"DRAFT","corpusMintedAt":null}}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"memory", "set", "--org", "acme.com", "--name", "Product specs", "--draft-corpus", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("create draft: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["CreateMemoryDraft"], &vars)
	if !strings.Contains(gen.CreateMemoryDraft_Operation, "draftCorpus: true") {
		t.Fatal("the draft-only operation must send draftCorpus:true")
	}
	if _, sent := vars["draftCorpus"]; sent {
		t.Errorf("the draft-only operation uses a literal argument, not a variable: %v", vars)
	}
	if !strings.Contains(out.String(), `"corpusState": "DRAFT"`) {
		t.Errorf("the created memory's state must be read back and echoed: %s", out.String())
	}

	// Without the flag, the ordinary operation has no draftCorpus argument at
	// all, so a pre-#1447 server can validate the document.
	gql, captured = captureGraphQL(t, map[string]string{"CreateMemory": created})
	f, _ = testFactory(t)
	root = NewRootCmd(f)
	root.SetArgs([]string{"memory", "set", "--org", "acme.com", "--name", "KB", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("create: %v", err)
	}
	if strings.Contains(gen.CreateMemory_Operation, "draftCorpus") {
		t.Fatal("ordinary creates must use an operation document with no draftCorpus argument")
	}
	if _, draftCreated := captured["CreateMemoryDraft"]; draftCreated {
		t.Error("an ordinary create must not call the draft-only operation")
	}
	// An ordinary create must not touch corpusState at all: a server without
	// draft corpora would reject it, and every create with it.
	if _, read := captured["SpecCorpusState"]; read {
		t.Errorf("an ordinary create must not read the corpus state")
	}
}

// The follow-up update does not select corpusState, so it must not erase the
// state that the draft-only read-back already established.
func TestMemorySetDraftCorpusKeepsStateAfterPostCreateUpdate(t *testing.T) {
	created := `{"data":{"createMemory":{"id":"m9","urn":"acme.com:product-specs","name":"Product specs",` +
		`"shortDescription":null,"class":"knowledge","visibility":"ORGANIZATION","organizationId":"o1",` +
		`"isEncrypted":false,"maxRevCount":10,"updatedAt":"2026-09-29T00:00:00Z"}}}`
	updated := strings.Replace(created, `"createMemory"`, `"updateMemory"`, 1)
	updated = strings.Replace(updated, "acme.com:product-specs", "acme.com:specs", 1)
	gql, captured := captureGraphQL(t, map[string]string{
		"CreateMemoryDraft": created,
		"SpecCorpusState": `{"data":{"memory":{"id":"m9","urn":"acme.com:product-specs",` +
			`"corpusState":"DRAFT","corpusMintedAt":null}}}`,
		"UpdateMemory": updated,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"memory", "set", "--org", "acme.com", "--name", "Product specs", "--draft-corpus", "--slug", "specs",
		"--schema", `{"objectTypes":{"insight":{"fields":{}}}}`, "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("create draft with post-create update: %v", err)
	}
	if _, called := captured["UpdateMemory"]; !called {
		t.Fatal("the slug/schema update must run")
	}
	if !strings.Contains(out.String(), `"corpusState": "DRAFT"`) || !strings.Contains(out.String(), `"urn": "acme.com:specs"`) {
		t.Errorf("the final JSON must retain the read-back state and updated URN: %s", out.String())
	}
}

func TestMemorySetDraftCorpusWarnsWhenStateReadBackFails(t *testing.T) {
	created := `{"data":{"createMemory":{"id":"m9","urn":"acme.com:product-specs","name":"Product specs",` +
		`"shortDescription":null,"class":"knowledge","visibility":"ORGANIZATION","organizationId":"o1",` +
		`"isEncrypted":false,"maxRevCount":10,"updatedAt":"2026-09-29T00:00:00Z"}}}`
	gql := fakeGraphQL(t, map[string]string{
		"CreateMemoryDraft": created,
		"SpecCorpusState":   `{"errors":[{"message":"state read failed","extensions":{"code":"INTERNAL_SERVER_ERROR"}}]}`,
	})
	f, _ := testFactory(t)
	errOut := f.IOStreams.ErrOut.(*strings.Builder)
	root := NewRootCmd(f)
	root.SetArgs([]string{"memory", "set", "--org", "acme.com", "--name", "Product specs", "--draft-corpus", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("the create succeeded, so the state read-back warning must not erase it: %v", err)
	}
	if !strings.Contains(errOut.String(), "was created, but its corpus state could not be read") {
		t.Errorf("the missing state must be disclosed on stderr: %s", errOut.String())
	}
}

// Draft is chosen only at creation, and only createMemory takes it: an update
// or an App-scoped create is refused before any request.
func TestMemorySetDraftCorpusRefusedOffline(t *testing.T) {
	for _, args := range [][]string{
		{"memory", "set", "acme.com:kb", "--draft-corpus"},
		{"memory", "set", "acme.com:kb", "--draft-corpus=false", "--name", "KB"},
		{"memory", "set", "--app", "hrn:app:acme.com:coach", "--agent", "hrn:agent:acme.com:a", "--class", "app", "--name", "X", "--draft-corpus"},
	} {
		f, _ := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs(append(args, "--server", "http://127.0.0.1:1"))
		if got := exitCodeFor(root.Execute()); got != exitcode.Usage {
			t.Errorf("%v should exit 2, got %d", args, got)
		}
	}
}

// Empty collections render as [], never null (stable-json-dto), for every
// array in the new DTOs.
func TestSpecCorpusEmptyCollectionsAreArrays(t *testing.T) {
	gql := fakeGraphQL(t, map[string]string{
		"Memories":                 memListJSON,
		"SpecUnresolvedReferences": `{"data":{"specUnresolvedReferences":[]}}`,
		"RenumberSpec":             `{"data":{"renumberSpec":{"dryRun":true,"moved":[],"rewrites":[],"textCitations":[]}}}`,
	})
	for _, c := range []struct {
		args []string
		keys []string
	}{
		{[]string{"spec", "unresolved"}, []string{`"references": []`}},
		{[]string{"spec", "renumber", "a:1", "a:2", "--dry-run"}, []string{`"moved": []`, `"rewrites": []`, `"textCitations": []`}},
	} {
		f, out := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs(append(c.args, "-m", "mem1", "--json", "--server", gql.URL))
		if err := root.Execute(); err != nil {
			t.Fatalf("%v: %v", c.args, err)
		}
		for _, k := range c.keys {
			if !strings.Contains(out.String(), k) {
				t.Errorf("%v: want %s in %s", c.args, k, out.String())
			}
		}
	}

	clean := `{"data":{"mintSpecCorpus":{"memoryId":"mem1","dryRun":true,"minted":false,"staleAbstractsBlock":false,` +
		`"blockers":[],"staleAbstracts":[],"openQuestions":[]}}}`
	url, _ := mintServer(t, clean, clean)
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "mint", "-m", "mem1", "--dry-run", "--json", "--server", url})
	if err := root.Execute(); err != nil {
		t.Fatalf("mint --dry-run: %v", err)
	}
	for _, k := range []string{`"blockers": []`, `"staleAbstracts": []`, `"openQuestions": []`} {
		if !strings.Contains(out.String(), k) {
			t.Errorf("mint: want %s in %s", k, out.String())
		}
	}
}

// An unset "$M" in `spec mint -m "$M" --yes` must not fall back to the
// ambient memory: the mint is irreversible. Refused before any request.
func TestSpecMintBlankMemoryRefused(t *testing.T) {
	url, calls := mintServer(t, mintReport(true, false, `[]`), mintReport(false, true, `[]`))
	f, _ := testFactory(t)
	// An ambient spec memory the fallback WOULD resolve to — without it, a
	// fallback also fails with a usage error and the guard goes unmeasured.
	t.Setenv("HADRON_SPEC_MEMORY", "mem1")
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "mint", "-m", " ", "--yes", "--server", url})
	if got := exitCodeFor(root.Execute()); got != exitcode.Usage {
		t.Errorf("a blank -m should exit 2, got %d", got)
	}
	if c := calls(); len(c) != 0 {
		t.Errorf("a blank -m must be refused before any request, got mint calls %v", c)
	}
}

// With --json on a terminal, stdout carries only the final document; the
// report the caller confirms against is shown on stderr, before the prompt.
func TestSpecMintJSONOnTTYShowsReportOnStderr(t *testing.T) {
	url, calls := mintServer(t, mintReport(true, false, `[]`), mintReport(false, true, `[]`))
	f, out, errOut := testFactoryTTY(t, "y\n")
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "mint", "-m", "mem1", "--json", "--server", url})
	if err := root.Execute(); err != nil {
		t.Fatalf("mint: %v", err)
	}
	if c := calls(); len(c) != 2 {
		t.Errorf("want the check and the mint, got %v", c)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(out.String()), &got); err != nil {
		t.Fatalf("stdout must be exactly one JSON document: %v (%q)", err, out.String())
	}
	e := errOut.String()
	if !strings.Contains(e, "Mint report") || strings.Index(e, "Mint report") > strings.Index(e, "one-way") {
		t.Errorf("the report must be on stderr before the prompt: %q", e)
	}
}

// ── slice 2: draft awareness in lint / list / get ──

const draftStateJSON = `{"data":{"memory":{"id":"mem1","urn":"micromentor.org:platform-specs","corpusState":"DRAFT","corpusMintedAt":null}}}`
const mintedStateJSON = `{"data":{"memory":{"id":"mem1","urn":"micromentor.org:platform-specs","corpusState":"MINTED","corpusMintedAt":null}}}`

func placeholderScanJSON(locs map[string]bool) string {
	var hits []string
	for loc, ph := range locs {
		b := "false"
		if ph {
			b = "true"
		}
		hits = append(hits, `{"node":{"loc":"`+loc+`","isPlaceholder":`+b+`}}`)
	}
	return `{"data":{"findNodes":{"hits":[` + strings.Join(hits, ",") + `]}}}`
}

// In a draft, a placeholder is reported once as `placeholder` — not linted as
// a malformed spec (this one is untagged, which would be a tag-spec ERROR) —
// and an unresolved reference is a warning at the citing spec, in scope only.
func TestSpecLintDraftReportsPlaceholdersAndUnresolved(t *testing.T) {
	const written, placeholder = "msg:010:02", "msg:010:03"
	responses := map[string]string{
		"FindNodes": `{"data":{"nodes":[` + specNodeList(written, `["spec","p1"]`) + `,` + specNodeList(placeholder, `[]`) + `]}}`,
		"NodeBatch": `{"data":{"nodeBatch":{"truncated":false,"omitted":[],"unavailable":[],"nodes":[` +
			specBatchNode(written) + `,` + specBatchNodeWithTags(placeholder, `[]`) + `]}}}`,
		"Memories":            memListMicromentorJSON,
		"GetMemory":           memGetVectorEnabledJSON,
		"SpecCorpusState":     draftStateJSON,
		"SpecPlaceholderScan": placeholderScanJSON(map[string]bool{written: false, placeholder: true}),
		"SpecUnresolvedReferences": `{"data":{"specUnresolvedReferences":[` +
			`{"kind":"URN","field":"content","reason":"MISSING","sourceLoc":"` + written + `","sourceNodeId":"n1","targetLoc":"msg:010:09","text":"x"},` +
			`{"kind":"EDGE","field":"edge","reason":"PLACEHOLDER","sourceLoc":"zzz:out:of:scope","sourceNodeId":"n2","targetLoc":"` + placeholder + `","text":"cites"}]}}`,
	}
	gql := fakeGraphQL(t, responses)
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "lint", "--all", "-m", specMem, "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("warnings only must exit 0, got %v\n%s", err, out.String())
	}
	var findings []map[string]any
	if err := json.Unmarshal([]byte(out.String()), &findings); err != nil {
		t.Fatalf("--json: %v (%q)", err, out.String())
	}
	var sawPlaceholder, sawUnresolved bool
	for _, fnd := range findings {
		switch {
		case fnd["citation"] == placeholder && fnd["rule"] == "placeholder" && fnd["severity"] == "warning":
			sawPlaceholder = true
		case fnd["citation"] == placeholder:
			t.Errorf("a placeholder must not be linted as a spec: %v", fnd)
		case fnd["rule"] == "unresolved-reference" && fnd["citation"] == written && fnd["severity"] == "warning":
			sawUnresolved = true
		case fnd["citation"] == "zzz:out:of:scope":
			t.Errorf("an unresolved reference from outside the linted scope must not be reported: %v", fnd)
		}
	}
	if !sawPlaceholder || !sawUnresolved {
		t.Errorf("want one placeholder and one in-scope unresolved-reference warning, got %s", out.String())
	}

	// --strict escalates them like any warning.
	f, _ = testFactory(t)
	root = NewRootCmd(f)
	root.SetArgs([]string{"spec", "lint", "--all", "-m", specMem, "--strict", "--server", gql.URL})
	if got := exitCodeFor(root.Execute()); got != exitcode.Conflict {
		t.Errorf("--strict must fail on the draft warnings, got %d", got)
	}
}

// A minted corpus holds no placeholders: after the one state read, neither
// the placeholder scan nor the reference scan is sent.
func TestSpecLintMintedCorpusSkipsTheDraftScans(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"FindNodes": `{"data":{"nodes":[` + specNodeList("msg:010:02", `["spec","p1"]`) + `]}}`,
		"NodeBatch": `{"data":{"nodeBatch":{"truncated":false,"omitted":[],"unavailable":[],"nodes":[` +
			specBatchNode("msg:010:02") + `]}}}`,
		"Memories":        memListMicromentorJSON,
		"GetMemory":       memGetVectorEnabledJSON,
		"SpecCorpusState": mintedStateJSON,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "lint", "--all", "-m", specMem, "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("lint: %v", err)
	}
	for _, op := range []string{"SpecPlaceholderScan", "SpecUnresolvedReferences"} {
		if _, sent := captured[op]; sent {
			t.Errorf("a minted corpus must not be scanned with %s", op)
		}
	}
}

func TestSpecListMarksPlaceholdersInADraft(t *testing.T) {
	gql := fakeGraphQL(t, map[string]string{
		"FindNodes":           `{"data":{"nodes":[` + specNodeList("msg:010:02", `["spec"]`) + `,` + specNodeList("msg:010:03", `["spec"]`) + `]}}`,
		"Memories":            memListMicromentorJSON,
		"SpecCorpusState":     draftStateJSON,
		"SpecPlaceholderScan": placeholderScanJSON(map[string]bool{"msg:010:02": false, "msg:010:03": true}),
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "list", "-m", specMem, "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("list: %v", err)
	}
	var specs []map[string]any
	if err := json.Unmarshal([]byte(out.String()), &specs); err != nil {
		t.Fatalf("--json: %v (%q)", err, out.String())
	}
	for _, s := range specs {
		want := s["citation"] == "msg:010:03"
		if (s["placeholder"] == true) != want {
			t.Errorf("placeholder marking wrong for %v", s)
		}
		if !want {
			if _, present := s["placeholder"]; present {
				t.Errorf("a written spec must not carry the key at all (omitempty): %v", s)
			}
		}
	}

	f, out = testFactory(t)
	root = NewRootCmd(f)
	root.SetArgs([]string{"spec", "list", "-m", specMem, "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("list: %v", err)
	}
	if !strings.Contains(out.String(), "[placeholder]") {
		t.Errorf("the table must mark the placeholder: %q", out.String())
	}
}

// A -m naming no memory must not read as an empty corpus (#799).
func TestSpecListMissingMemoryIsNotFound(t *testing.T) {
	gql := fakeGraphQL(t, map[string]string{
		"FindNodes":       `{"data":{"nodes":[]}}`,
		"Memories":        memListMicromentorJSON,
		"SpecCorpusState": `{"data":{"memory":null}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "list", "-m", "hrn:mem:mentor-co:does-not-exist", "--server", gql.URL})
	err := root.Execute()
	if err == nil {
		t.Fatal("want not-found error, got nil")
	}
	if got := exitCodeFor(err); got != exitcode.NotFound {
		t.Errorf("exit code = %d, want %d (%v)", got, exitcode.NotFound, err)
	}
}
