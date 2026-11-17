package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// hadron-cli#1340 (cor:api:240:02): memory merge, memory verify, and the
// memory settings memory create|set used to drop. Each test is red on main
// because the flag or command does not exist, or the variable is not sent.

const mergedMemoryJSON = `{"id":"m2","urn":"hrn:mem:acme.com:kb","name":"KB","shortDescription":null,
	"class":"knowledge","visibility":"ORGANIZATION","organizationId":"o1",
	"isEncrypted":false,"maxRevCount":10,"updatedAt":"2026-06-11T00:00:00Z"}`

func run1340(t *testing.T, responses map[string]string, args ...string) (map[string]json.RawMessage, string, error) {
	t.Helper()
	gql, captured := captureGraphQL(t, responses)
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append(args, "--server", gql.URL))
	err := root.Execute()
	return captured, out.String(), err
}

func vars1340(t *testing.T, captured map[string]json.RawMessage, op string) map[string]any {
	t.Helper()
	raw, ok := captured[op]
	if !ok {
		t.Fatalf("%s was not sent; sent %v", op, keys1340(captured))
	}
	var v map[string]any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("decode %s variables: %v", op, err)
	}
	return v
}

func keys1340(m map[string]json.RawMessage) []string {
	out := []string{}
	for k := range m {
		out = append(out, k)
	}
	return out
}

var set1340Responses = map[string]string{
	"GetMemory":         `{"data":{"memory":` + memoryJSON + `}}`,
	"UpdateMemory":      `{"data":{"updateMemory":` + memoryListedJSON(false) + `}}`,
	"CreateMemory":      `{"data":{"createMemory":` + memoryJSON + `}}`,
	"CreateMemoryInApp": `{"data":{"createMemoryInApp":` + memoryJSON + `}}`,
}

// Every setting flag reaches updateMemory with the value given.
func TestMemorySetSendsEverySetting1340(t *testing.T) {
	captured, out, err := run1340(t, set1340Responses, "memory", "set", "acme.com::kb",
		"--category0", "Science", "--category1", "Physics", "--category2", "Optics",
		"--license", "CC-BY-4.0", "--icon-url", "https://x.test/i.png", "--hero-url", "https://x.test/h.png",
		"--home-url", "https://x.test", "--git-source", "https://github.com/acme/kb",
		"--read-branch", "main", "--write-branch", "sync",
		"--data", `{"k":1}`, "--embedding-source", "contentChunks", "--vector-index",
		"--chunk-tokens", "400", "--chunk-overlap", "40", "--force-fixed-size",
		"--acknowledge-vector-inversion-risk")
	if err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}
	v := vars1340(t, captured, "UpdateMemory")
	want := map[string]any{
		"category0": "Science", "category1": "Physics", "category2": "Optics",
		"license": "CC-BY-4.0", "iconUrl": "https://x.test/i.png", "heroUrl": "https://x.test/h.png",
		"homeUrl": "https://x.test", "source": "https://github.com/acme/kb",
		"readBranch": "main", "writeBranch": "sync",
		"embeddingSource": "contentChunks", "vectorIndexEnabled": true,
		"chunkTokens": float64(400), "chunkOverlap": float64(40), "forceFixedSize": true,
		"acknowledgeVectorInversionRisk": true,
	}
	for k, w := range want {
		if got, ok := v[k]; !ok || got != w {
			t.Errorf("%s = %v (sent %v), want %v", k, got, ok, w)
		}
	}
	if d, _ := json.Marshal(v["data"]); string(d) != `{"k":1}` {
		t.Errorf("data = %s, want {\"k\":1}", d)
	}
}

// An omitted flag is omitted on the wire (the server keeps the value); an
// explicit empty value is an explicit null (the server clears it).
func TestMemorySetOmitsUnpassedSettingsAndClearsEmptyOnes1340(t *testing.T) {
	captured, _, err := run1340(t, set1340Responses, "memory", "set", "acme.com::kb", "--license", "", "--git-source=", "--data", "null")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	v := vars1340(t, captured, "UpdateMemory")
	for _, k := range []string{"license", "source", "data"} {
		if got, ok := v[k]; !ok || got != nil {
			t.Errorf("%s: an empty value must be sent as null (clear), got %v (sent %v)", k, got, ok)
		}
	}
	for _, k := range []string{"category0", "category1", "category2", "iconUrl", "heroUrl", "homeUrl", "readBranch", "writeBranch",
		"embeddingSource", "vectorIndexEnabled", "chunkTokens", "chunkOverlap", "forceFixedSize", "acknowledgeVectorInversionRisk"} {
		if got, ok := v[k]; ok {
			t.Errorf("%s was not passed and must stay omitted, got %v", k, got)
		}
	}
}

// The off spellings send false, and contradictory pairs refuse before any write.
func TestMemorySetBooleanPairs1340(t *testing.T) {
	captured, _, err := run1340(t, set1340Responses, "memory", "set", "acme.com::kb", "--no-vector-index", "--no-force-fixed-size")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	v := vars1340(t, captured, "UpdateMemory")
	if v["vectorIndexEnabled"] != false || v["forceFixedSize"] != false {
		t.Errorf("--no-* must send false: %v", v)
	}
	for _, pair := range [][]string{{"--vector-index", "--no-vector-index"}, {"--force-fixed-size", "--no-force-fixed-size"}} {
		captured, _, err := run1340(t, set1340Responses, append([]string{"memory", "set", "acme.com::kb"}, pair...)...)
		if exitCodeFor(err) != exitcode.Usage {
			t.Errorf("%v: want exit 2, got %v", pair, err)
		}
		if _, sent := captured["UpdateMemory"]; sent {
			t.Errorf("%v: a contradiction must not write", pair)
		}
	}
}

// The settings ride on createMemory too; an empty value has nothing to clear
// on create and is refused rather than dropped.
func TestMemoryCreateSendsSettings1340(t *testing.T) {
	captured, out, err := run1340(t, set1340Responses, "memory", "create", "--org", "acme.com", "--name", "KB",
		"--category0", "Science", "--license", "MIT", "--git-source", "https://github.com/acme/kb", "--read-branch", "main",
		"--write-branch", "sync", "--icon-url", "https://x.test/i.png", "--hero-url", "https://x.test/h.png", "--home-url", "https://x.test",
		"--category1", "a", "--category2", "b",
		"--data", `{"k":1}`, "--embedding-source", "abstract", "--no-vector-index")
	if err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}
	v := vars1340(t, captured, "CreateMemory")
	for k, w := range map[string]any{"category0": "Science", "category1": "a", "category2": "b", "license": "MIT",
		"source": "https://github.com/acme/kb", "readBranch": "main", "writeBranch": "sync", "iconUrl": "https://x.test/i.png",
		"heroUrl": "https://x.test/h.png", "homeUrl": "https://x.test", "embeddingSource": "abstract", "vectorIndexEnabled": false} {
		if got, ok := v[k]; !ok || got != w {
			t.Errorf("%s = %v (sent %v), want %v", k, got, ok, w)
		}
	}
	if d, _ := json.Marshal(v["data"]); string(d) != `{"k":1}` {
		t.Errorf("data = %s", d)
	}

	captured, _, err = run1340(t, set1340Responses, "memory", "create", "--org", "acme.com", "--name", "KB", "--license", "")
	if exitCodeFor(err) != exitcode.Usage {
		t.Fatalf("an empty setting on create must exit 2, got %v", err)
	}
	if _, sent := captured["CreateMemory"]; sent {
		t.Fatalf("a refused create must not be sent")
	}
	// Update-only dials are refused on create, before anything is sent.
	for _, flag := range []string{"--chunk-tokens=400", "--chunk-overlap=4", "--force-fixed-size", "--acknowledge-vector-inversion-risk"} {
		captured, _, err = run1340(t, set1340Responses, "memory", "create", "--org", "acme.com", "--name", "KB", flag)
		if exitCodeFor(err) != exitcode.Usage {
			t.Errorf("%s on create: want exit 2, got %v", flag, err)
		}
		if _, sent := captured["CreateMemory"]; sent {
			t.Errorf("%s on create: nothing may be sent", flag)
		}
	}
}

// createMemoryInApp takes data, embedding source and vector index; the other
// settings are refused there, never dropped.
func TestMemoryCreateInAppSettings1340(t *testing.T) {
	base := []string{"memory", "create", "--app", "hrn:app:acme.com:coach", "--agent", "hrn:agent:acme.com:a", "--class", "app", "--name", "R"}
	captured, out, err := run1340(t, set1340Responses, append(base, "--data", `{"k":1}`, "--embedding-source", "both", "--vector-index")...)
	if err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}
	v := vars1340(t, captured, "CreateMemoryInApp")
	if v["embeddingSource"] != "both" || v["vectorIndexEnabled"] != true {
		t.Errorf("in-app settings not sent: %v", v)
	}
	if d, _ := json.Marshal(v["data"]); string(d) != `{"k":1}` {
		t.Errorf("data = %s", d)
	}
	captured, _, err = run1340(t, set1340Responses, append(base, "--license", "MIT")...)
	if exitCodeFor(err) != exitcode.Usage {
		t.Fatalf("--license with --app/--agent must exit 2, got %v", err)
	}
	if _, sent := captured["CreateMemoryInApp"]; sent {
		t.Fatalf("a refused in-app create must not be sent")
	}
}

func TestMemoryDataFile1340(t *testing.T) {
	p := filepath.Join(t.TempDir(), "d.json")
	if err := os.WriteFile(p, []byte(`{"from":"file"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	captured, _, err := run1340(t, set1340Responses, "memory", "set", "acme.com::kb", "--data-file", p)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if d, _ := json.Marshal(vars1340(t, captured, "UpdateMemory")["data"]); string(d) != `{"from":"file"}` {
		t.Errorf("data = %s", d)
	}
	_, _, err = run1340(t, set1340Responses, "memory", "set", "acme.com::kb", "--data", "{bad")
	if exitCodeFor(err) != exitcode.Usage {
		t.Errorf("malformed --data must exit 2, got %v", err)
	}
}

func TestMemoryMerge1340(t *testing.T) {
	responses := map[string]string{"MergeMemories": `{"data":{"mergeMemories":` + mergedMemoryJSON + `}}`}
	captured, out, err := run1340(t, responses, "memory", "merge", "acme.com:old-kb", "--into", "hrn:mem:acme.com:kb",
		"--field", "content", "--field", "TAGS", "--delete-source", "--yes", "--json")
	if err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}
	in, _ := vars1340(t, captured, "MergeMemories")["input"].(map[string]any)
	if in["source"] != "hrn:mem:acme.com:old-kb" || in["target"] != "hrn:mem:acme.com:kb" || in["deleteSourceMem"] != true {
		t.Errorf("merge input: %v", in)
	}
	if inc, _ := json.Marshal(in["include"]); string(inc) != `["CONTENT","TAGS"]` {
		t.Errorf("include = %s", inc)
	}
	var dto map[string]any
	if err := json.Unmarshal([]byte(out), &dto); err != nil || dto["urn"] != "hrn:mem:acme.com:kb" {
		t.Errorf("--json receipt: %v %s", err, out)
	}

	// No --field: every field (include omitted); no --delete-source: not sent as true.
	captured, _, err = run1340(t, responses, "memory", "merge", "acme.com:old-kb", "--into", "acme.com:kb", "--yes")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	in, _ = vars1340(t, captured, "MergeMemories")["input"].(map[string]any)
	// cli#1476 (Coco QA note, Diego LOCAL P3): include is absent, not null;
	// deleteSourceMem is present and false, never left to a server default.
	if inc, ok := in["include"]; ok {
		t.Errorf("include must be absent without --field, got %v", inc)
	}
	if del, ok := in["deleteSourceMem"]; !ok || del != false {
		t.Errorf("deleteSourceMem must be sent as false without --delete-source, got %v (sent %v)", del, ok)
	}

	// Non-interactive without --yes: exit 2, nothing sent.
	captured, _, err = run1340(t, responses, "memory", "merge", "acme.com:old-kb", "--into", "acme.com:kb")
	if exitCodeFor(err) != exitcode.Usage {
		t.Fatalf("merge without --yes non-interactively must exit 2, got %v", err)
	}
	if _, sent := captured["MergeMemories"]; sent {
		t.Fatalf("an unconfirmed merge must not be sent")
	}
	// Missing --into, or an empty one, is a usage error.
	for _, args := range [][]string{{"memory", "merge", "acme.com:old-kb", "--yes"}, {"memory", "merge", "acme.com:old-kb", "--into=", "--yes"}} {
		captured, _, err = run1340(t, responses, args...)
		if exitCodeFor(err) != exitcode.Usage {
			t.Errorf("%v: want exit 2, got %v", args, err)
		}
		if _, sent := captured["MergeMemories"]; sent {
			t.Errorf("%v: nothing may be sent", args)
		}
	}
}

// A refused merge maps through api.MapError and keeps the server's code.
func TestMemoryMergeRefusalIsMapped1340(t *testing.T) {
	responses := map[string]string{"MergeMemories": `{"data":null,"errors":[{"message":"mergeMemories does not support encrypted memories yet.","extensions":{"code":"BAD_USER_INPUT"}}]}`}
	_, _, err := run1340(t, responses, "memory", "merge", "acme.com:old-kb", "--into", "acme.com:kb", "--yes")
	if err == nil || exitCodeFor(err) == exitcode.OK || !strings.Contains(err.Error(), "encrypted") {
		t.Fatalf("refusal must surface with its message and a non-zero exit, got %v", err)
	}
}

func TestMemoryVerify1340(t *testing.T) {
	clean := `{"data":{"verifyMemoryNodes":{"checked":3,"intact":2,"notApproved":1,"superseded":0,"tampered":0,"problems":[
		{"nodeId":"n3","urn":"hrn:node:acme.com:kb:c","loc":"c","state":"NOT_APPROVED","revision":1,"approvedRevision":null,"expectedHash":null,"actualHash":"h3"}]}}}`
	captured, out, err := run1340(t, map[string]string{"VerifyMemoryNodes": clean}, "memory", "verify", "acme.com:kb", "--json")
	if err != nil {
		t.Fatalf("no tampered node must exit 0, got %v\n%s", err, out)
	}
	if v := vars1340(t, captured, "VerifyMemoryNodes"); v["memoryRef"] != "hrn:mem:acme.com:kb" {
		t.Errorf("memoryRef = %v", v["memoryRef"])
	}
	var dto map[string]any
	if err := json.Unmarshal([]byte(out), &dto); err != nil {
		t.Fatalf("--json: %v\n%s", err, out)
	}
	if dto["checked"] != float64(3) || dto["tampered"] != float64(0) {
		t.Errorf("counts: %v", dto)
	}
	if p, _ := dto["problems"].([]any); len(p) != 1 {
		t.Errorf("problems: %v", dto["problems"])
	}

	empty := `{"data":{"verifyMemoryNodes":{"checked":0,"intact":0,"notApproved":0,"superseded":0,"tampered":0,"problems":[]}}}`
	_, out, _ = run1340(t, map[string]string{"VerifyMemoryNodes": empty}, "memory", "verify", "acme.com:kb", "--json")
	if !strings.Contains(out, `"problems": []`) && !strings.Contains(out, `"problems":[]`) {
		t.Errorf("problems must be [] not null: %s", out)
	}

	tampered := `{"data":{"verifyMemoryNodes":{"checked":2,"intact":1,"notApproved":0,"superseded":0,"tampered":1,"problems":[
		{"nodeId":"n2","urn":"hrn:node:acme.com:kb:b","loc":"b","state":"TAMPERED","revision":4,"approvedRevision":4,"expectedHash":"e","actualHash":"a"}]}}}`
	_, out, err = run1340(t, map[string]string{"VerifyMemoryNodes": tampered}, "memory", "verify", "acme.com:kb")
	if exitCodeFor(err) != exitcode.Conflict {
		t.Fatalf("a tampered node must exit 5, got %v", err)
	}
	if !strings.Contains(out, "TAMPERED") || !strings.Contains(out, "hrn:node:acme.com:kb:b") {
		t.Errorf("human output must name the tampered node: %s", out)
	}
}

func TestMemoryListOrgAndVisibility1340(t *testing.T) {
	responses := map[string]string{"Memories": `{"data":{"memories":{"total":1,"items":[` + memoryJSON + `]}}}`}
	captured, _, err := run1340(t, responses, "memory", "list", "--org", "acme.com", "--visibility", "public", "--json")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	v := vars1340(t, captured, "Memories")
	if v["orgId"] != "acme.com" {
		t.Errorf("orgId = %v", v["orgId"])
	}
	if f, _ := v["filter"].(map[string]any); f == nil || f["visibility"] != "PUBLIC" {
		t.Errorf("filter.visibility = %v", v["filter"])
	}
	for _, args := range [][]string{{"--org="}, {"--visibility", "NOPE"}, {"--shared-with-me", "--org", "acme.com"}} {
		captured, _, err := run1340(t, responses, append([]string{"memory", "list"}, args...)...)
		if exitCodeFor(err) != exitcode.Usage {
			t.Errorf("%v: want exit 2, got %v", args, err)
		}
		if _, sent := captured["Memories"]; sent {
			t.Errorf("%v: nothing may be sent", args)
		}
	}
}

func TestScopeListAppContext1340(t *testing.T) {
	responses := map[string]string{"Scopes": `{"data":{"scopes":{"total":0,"items":[]}}}`}
	captured, _, err := run1340(t, responses, "scope", "list", "--app-context", "hrn:app:acme.com:coach", "--json")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	f, _ := vars1340(t, captured, "Scopes")["filter"].(map[string]any)
	if f == nil || f["appRef"] != "hrn:app:acme.com:coach" {
		t.Errorf("filter.appRef = %v", f)
	}
	captured, _, err = run1340(t, responses, "scope", "list", "--app-context=")
	if exitCodeFor(err) != exitcode.Usage {
		t.Errorf("empty --app-context must exit 2, got %v", err)
	}
	if _, sent := captured["Scopes"]; sent {
		t.Errorf("empty --app-context: nothing may be sent")
	}
}

func TestMemoryEncryptPassphrase1340(t *testing.T) {
	responses := map[string]string{
		"GetMemory":     `{"data":{"memory":` + memoryJSON + `}}`,
		"EncryptMemory": `{"data":{"encryptMemory":{"id":"m1","urn":"hrn:mem:jane:diary","name":"Diary","isEncrypted":true}}}`,
	}
	p := filepath.Join(t.TempDir(), "pass")
	if err := os.WriteFile(p, []byte("correct horse battery staple\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	captured, out, err := run1340(t, responses, "memory", "encrypt", "jane:diary", "--passphrase-file", p, "--yes", "--json")
	if err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}
	v := vars1340(t, captured, "EncryptMemory")
	if v["passphrase"] != "correct horse battery staple" {
		t.Errorf("passphrase not sent (trailing newline trimmed): %v", v["passphrase"])
	}
	if _, ok := v["dataKey"]; ok {
		t.Errorf("dataKey must be omitted with a passphrase: %v", v)
	}
	if strings.Contains(out, "correct horse") {
		t.Errorf("the passphrase must never be echoed: %s", out)
	}
	for _, args := range [][]string{
		{"--passphrase-file", p, "--data-key", "k", "--yes"},
		{"--yes"},
		{"--passphrase-file=", "--yes"},
	} {
		captured, _, err := run1340(t, responses, append([]string{"memory", "encrypt", "jane:diary"}, args...)...)
		if exitCodeFor(err) != exitcode.Usage {
			t.Errorf("%v: want exit 2, got %v", args, err)
		}
		if _, sent := captured["EncryptMemory"]; sent {
			t.Errorf("%v: nothing may be sent", args)
		}
	}
}

// cli#1476 BLIND P2 (Bruno): an empty --data-file names no file; it is
// refused, never read as an empty --data that clears the data.
func TestMemoryEmptyDataFileRefused1476(t *testing.T) {
	for _, args := range [][]string{
		{"memory", "set", "acme.com::kb", "--data-file="},
		{"memory", "set", "acme.com::kb", "--data-file", "  "},
		{"memory", "create", "--org", "acme.com", "--name", "KB", "--data-file="},
	} {
		captured, _, err := run1340(t, set1340Responses, args...)
		if exitCodeFor(err) != exitcode.Usage {
			t.Errorf("%v: want exit 2, got %v", args, err)
		}
		for _, op := range []string{"UpdateMemory", "CreateMemory"} {
			if _, sent := captured[op]; sent {
				t.Errorf("%v: %s must not be sent", args, op)
			}
		}
	}
}

// cli#1476 BLIND P2 (Bruno): an explicit =false is a value, not an absent
// flag. --x=false sends false, --no-x=false sends true, and a pair that
// disagrees with itself is refused before any write.
func TestMemoryExplicitFalseBooleans1476(t *testing.T) {
	cases := []struct {
		args []string
		want map[string]any
	}{
		{[]string{"--vector-index=false", "--force-fixed-size=false"}, map[string]any{"vectorIndexEnabled": false, "forceFixedSize": false}},
		{[]string{"--no-vector-index=false", "--no-force-fixed-size=false"}, map[string]any{"vectorIndexEnabled": true, "forceFixedSize": true}},
		{[]string{"--vector-index=false", "--no-vector-index"}, map[string]any{"vectorIndexEnabled": false}},
		{[]string{"--acknowledge-vector-inversion-risk=false"}, map[string]any{"acknowledgeVectorInversionRisk": false}},
	}
	for _, c := range cases {
		captured, out, err := run1340(t, set1340Responses, append([]string{"memory", "set", "acme.com::kb"}, c.args...)...)
		if err != nil {
			t.Errorf("%v: execute: %v\n%s", c.args, err, out)
			continue
		}
		v := vars1340(t, captured, "UpdateMemory")
		for k, w := range c.want {
			if got, ok := v[k]; !ok || got != w {
				t.Errorf("%v: %s = %v (sent %v), want %v", c.args, k, got, ok, w)
			}
		}
	}
	captured, out, err := run1340(t, set1340Responses, "memory", "create", "--org", "acme.com", "--name", "KB", "--vector-index=false")
	if err != nil {
		t.Fatalf("create: %v\n%s", err, out)
	}
	if got, ok := vars1340(t, captured, "CreateMemory")["vectorIndexEnabled"]; !ok || got != false {
		t.Errorf("create --vector-index=false: vectorIndexEnabled = %v (sent %v), want false", got, ok)
	}
	for _, pair := range [][]string{
		{"--vector-index=false", "--no-vector-index=false"},
		{"--force-fixed-size", "--no-force-fixed-size=true"},
	} {
		captured, _, err := run1340(t, set1340Responses, append([]string{"memory", "set", "acme.com::kb"}, pair...)...)
		if exitCodeFor(err) != exitcode.Usage {
			t.Errorf("%v: want exit 2, got %v", pair, err)
		}
		if _, sent := captured["UpdateMemory"]; sent {
			t.Errorf("%v: a contradiction must not write", pair)
		}
	}
}
