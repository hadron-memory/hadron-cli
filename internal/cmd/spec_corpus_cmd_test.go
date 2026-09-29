package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

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
	if !strings.Contains(errOut.String(), "can never return to draft") {
		t.Errorf("the prompt must say the mint is one-way: %q", errOut.String())
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
		"CreateMemory": created,
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
	_ = json.Unmarshal(captured["CreateMemory"], &vars)
	if vars["draftCorpus"] != true {
		t.Errorf("--draft-corpus must send draftCorpus:true, got %v", vars)
	}
	if !strings.Contains(out.String(), `"corpusState": "DRAFT"`) {
		t.Errorf("the created memory's state must be read back and echoed: %s", out.String())
	}

	// Without the flag, draftCorpus is omitted — the server's default (minted)
	// applies, and an older server never sees an argument it doesn't know.
	gql, captured = captureGraphQL(t, map[string]string{"CreateMemory": created})
	f, _ = testFactory(t)
	root = NewRootCmd(f)
	root.SetArgs([]string{"memory", "set", "--org", "acme.com", "--name", "KB", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("create: %v", err)
	}
	vars = nil
	_ = json.Unmarshal(captured["CreateMemory"], &vars)
	if _, sent := vars["draftCorpus"]; sent {
		t.Errorf("an unset --draft-corpus must be omitted, got %v", vars)
	}
	// An ordinary create must not touch corpusState at all: a server without
	// draft corpora would reject it, and every create with it.
	if _, read := captured["SpecCorpusState"]; read {
		t.Errorf("an ordinary create must not read the corpus state")
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
	if !strings.Contains(e, "Mint report") || strings.Index(e, "Mint report") > strings.Index(e, "can never return to draft") {
		t.Errorf("the report must be on stderr before the prompt: %q", e)
	}
}
