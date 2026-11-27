package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// hadron-cli#3072 (Billing B2): a person's ledger, owner limits, and the
// OWNER_LIMIT_REACHED / NODE_CONTENT_TOO_LARGE refusals. Every pin here is red on
// main for the reason its comment gives.

const annUser3072 = `{"id":"usr_ann","urn":"hrn:user:ann","name":"Ann","email":"ann@acme.com","handle":"ann","githubUsername":null,"roles":[]}`

func personBalance3072() map[string]string {
	return map[string]string{
		"GetUser":       `{"data":{"user":` + annUser3072 + `}}`,
		"CreditBalance": `{"data":{"creditBalance":{"organizationId":null,"ownerUserId":"usr_ann","balanceMicroCents":"250","expiring":[]}}}`,
	}
}

func jsonObject3072(t *testing.T, s string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		t.Fatalf("not a JSON object: %v\n%s", err, s)
	}
	return m
}

// 1: balance --user sends userRef and no orgRef; the DTO names the person.
func TestBillingBalanceForAPerson3072(t *testing.T) {
	out, captured, err := runBilling(t, personBalance3072(), "billing", "balance", "--user", "@ann", "--json")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	vars := billingVars(t, captured["CreditBalance"])
	if vars["userRef"] != "usr_ann" {
		t.Errorf("userRef = %v, want Ann's id", vars["userRef"])
	}
	if _, sent := vars["orgRef"]; sent {
		t.Errorf("orgRef must be omitted, never null: %v", vars)
	}
	dto := jsonObject3072(t, out)
	if v, present := dto["organizationId"]; !present || v != nil {
		t.Errorf(`"organizationId" must be present and null for a person: %v`, dto)
	}
	if dto["ownerUserId"] != "usr_ann" {
		t.Errorf("ownerUserId = %v", dto["ownerUserId"])
	}
	text, _, err := runBilling(t, personBalance3072(), "billing", "balance", "--user", "@ann")
	if err != nil || !strings.Contains(text, "(user usr_ann)") {
		t.Errorf("text must name the person: %q (%v)", text, err)
	}
}

// 2: an organization's balance keeps its keys and gains ownerUserId: null.
func TestBillingBalanceForAnOrgCarriesOwnerUserIdNull3072(t *testing.T) {
	resp := map[string]string{"CreditBalance": `{"data":{"creditBalance":{"organizationId":"org1","ownerUserId":null,"balanceMicroCents":"5","expiring":[]}}}`}
	out, captured, err := runBilling(t, resp, "billing", "balance", "hrn:org:acme.com", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if v := billingVars(t, captured["CreditBalance"]); v["orgRef"] != "hrn:org:acme.com" {
		t.Errorf("orgRef = %v", v)
	} else if _, sent := v["userRef"]; sent {
		t.Errorf("userRef must be omitted: %v", v)
	}
	dto := jsonObject3072(t, out)
	if v, present := dto["ownerUserId"]; !present || v != nil {
		t.Errorf(`"ownerUserId" must be present and null for an organization: %v`, dto)
	}
	if dto["organizationId"] != "org1" || dto["balanceMicroCents"] != "5" {
		t.Errorf("every existing key stays: %v", dto)
	}
}

// 3: --owner-me reads the caller once and sends that id; a credential that is no
// person is refused (exit 8) before any billing request.
func TestBillingOwnerMe3072(t *testing.T) {
	resp := personBalance3072()
	resp["Me"] = `{"data":{"me":{"id":"usr_me","name":"Me","email":"me@acme.com","handle":"me","githubUsername":null,"roles":[]}}}`
	_, captured, err := runBilling(t, resp, "billing", "balance", "--owner-me", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if v := billingVars(t, captured["CreditBalance"]); v["userRef"] != "usr_me" {
		t.Errorf("userRef = %v, want the caller's id", v["userRef"])
	}
	resp["Me"] = `{"data":{"me":null}}`
	_, captured, err = runBilling(t, resp, "billing", "balance", "--owner-me")
	if got := exitCodeFor(err); got != exitcode.Forbidden {
		t.Errorf("a credential that is not a person must exit 8, got %d (%v)", got, err)
	}
	if _, sent := captured["CreditBalance"]; sent {
		t.Error("nothing may be sent to billing when --owner-me cannot resolve")
	}
}

// 4: exactly one owner, on all six commands.
func TestBillingNamesExactlyOneOwner3072(t *testing.T) {
	cmds := map[string][]string{
		"balance": {"billing", "balance"},
		"ledger":  {"billing", "ledger"},
		"grant":   {"billing", "grant", "--amount", "5", "--source-type", "t", "--source-id", "i", "--yes"},
		"adjust":  {"billing", "adjust", "--amount", "5", "--source-type", "t", "--source-id", "i", "--note", "n", "--yes"},
		"spend":   {"billing", "spend", "--from", "2026-11-01", "--to", "2026-12-01", "--group-by", "day"},
		"limits":  {"billing", "limits"},
	}
	cases := map[string][]string{
		"no owner":                  nil,
		"positional and --user":     {"acme", "--user", "ann"},
		"--user and --owner-me":     {"--user", "ann", "--owner-me"},
		"positional and --owner-me": {"acme", "--owner-me"},
		"an empty --user":           {"--user="},
		"an empty positional":       {""},
	}
	for name, base := range cmds {
		for label, extra := range cases {
			t.Run(name+"/"+label, func(t *testing.T) {
				args := append(append([]string{}, base[:2]...), extra...)
				args = append(args, base[2:]...)
				_, captured, err := runBilling(t, personBalance3072(), args...)
				if got := exitCodeFor(err); got != exitcode.Usage {
					t.Fatalf("%v: want exit 2, got %d (%v)", args, got, err)
				}
				if len(captured) != 0 {
					t.Errorf("%v: nothing may be sent: %v", args, captured)
				}
			})
		}
	}
}

// 5: ledger for a person pages as today and each entry carries ownerUserId.
func TestBillingLedgerForAPerson3072(t *testing.T) {
	entry := `{"id":"l1","organizationId":null,"ownerUserId":"usr_ann","kind":"GRANT","amountMicroCents":"7","expiresAt":null,"sourceType":null,"sourceId":null,"note":null,"createdAt":"2026-07-11T00:00:00.000Z","createdBy":null}`
	resp := map[string]string{
		"GetUser":             `{"data":{"user":` + annUser3072 + `}}`,
		"CreditLedgerEntries": `{"data":{"creditLedgerEntries":{"total":1,"items":[` + entry + `]}}}`,
	}
	out, captured, err := runBilling(t, resp, "billing", "ledger", "--user", "ann", "--json")
	if err != nil {
		t.Fatal(err)
	}
	vars := billingVars(t, captured["CreditLedgerEntries"])
	if vars["userRef"] != "usr_ann" {
		t.Errorf("userRef = %v", vars["userRef"])
	}
	if _, sent := vars["orgRef"]; sent {
		t.Errorf("orgRef must be omitted: %v", vars)
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(out), &rows); err != nil || len(rows) != 1 {
		t.Fatalf("%v\n%s", err, out)
	}
	if v, present := rows[0]["organizationId"]; !present || v != nil || rows[0]["ownerUserId"] != "usr_ann" {
		t.Errorf("a person's entry names the person and a null organization: %v", rows[0])
	}
}

// 6: grant, adjust and spend for a person send userRef and omit orgRef.
func TestBillingWritesAndSpendForAPerson3072(t *testing.T) {
	entry := `{"id":"l1","organizationId":null,"ownerUserId":"usr_ann","kind":"GRANT","amountMicroCents":"5","expiresAt":null,"sourceType":"t","sourceId":"i","note":null,"createdAt":"2026-07-11T00:00:00.000Z","createdBy":null}`
	user := `{"data":{"user":` + annUser3072 + `}}`
	for _, tc := range []struct {
		op   string
		resp map[string]string
		args []string
	}{
		{"GrantCredits", map[string]string{"GetUser": user, "GrantCredits": `{"data":{"grantCredits":` + entry + `}}`}, []string{"billing", "grant", "--user", "ann", "--amount", "5", "--source-type", "t", "--source-id", "i", "--yes", "--json"}},
		{"AdjustCredits", map[string]string{"GetUser": user, "AdjustCredits": `{"data":{"adjustCredits":` + entry + `}}`}, []string{"billing", "adjust", "--user", "ann", "--amount", "5", "--source-type", "t", "--source-id", "i", "--note", "n", "--yes", "--json"}},
	} {
		t.Run(tc.op, func(t *testing.T) {
			out, captured, err := runBilling(t, tc.resp, tc.args...)
			if err != nil {
				t.Fatal(err)
			}
			in := billingInputOf(t, captured, tc.op)
			if in["userRef"] != "usr_ann" {
				t.Errorf("userRef = %v", in["userRef"])
			}
			if _, sent := in["orgRef"]; sent {
				t.Errorf("orgRef must be omitted, never null: %v", in)
			}
			if dto := jsonObject3072(t, out); dto["ownerUserId"] != "usr_ann" {
				t.Errorf("DTO: %v", dto)
			}
		})
	}
	spend := map[string]string{"GetUser": user, "AiSpend": `{"data":{"aiSpend":{"from":"2026-11-01T00:00:00Z","to":"2026-12-01T00:00:00Z","truncated":false,"totalGroups":0,"rows":[]}}}`}
	_, captured, err := runBilling(t, spend, "billing", "spend", "--user", "ann", "--from", "2026-11-01", "--to", "2026-12-01", "--group-by", "day", "--json")
	if err != nil {
		t.Fatal(err)
	}
	vars := billingVars(t, captured["AiSpend"])
	if vars["userRef"] != "usr_ann" {
		t.Errorf("userRef = %v", vars["userRef"])
	}
	if _, sent := vars["orgRef"]; sent {
		t.Errorf("orgRef must be omitted: %v", vars)
	}
}

const limitsResp3072 = `{"data":{"ownerLimits":[{"key":"nodes","limit":"20000","used":"3"},{"key":"apps","limit":null,"used":"0"}]}}`

// 7: limits read: one element per key the server returns, unlimited as null.
func TestBillingLimitsRead3072(t *testing.T) {
	out, captured, err := runBilling(t, map[string]string{"OwnerLimits": limitsResp3072}, "billing", "limits", "hrn:org:acme.com", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if v := billingVars(t, captured["OwnerLimits"]); v["orgRef"] != "hrn:org:acme.com" {
		t.Errorf("vars = %v", v)
	} else if _, sent := v["userRef"]; sent {
		t.Errorf("userRef must be omitted: %v", v)
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(out), &rows); err != nil || len(rows) != 2 {
		t.Fatalf("a bare array of two: %v\n%s", err, out)
	}
	if rows[0]["key"] != "nodes" || rows[0]["limit"] != "20000" || rows[0]["used"] != "3" {
		t.Errorf("row 0: %v", rows[0])
	}
	if v, present := rows[1]["limit"]; !present || v != nil {
		t.Errorf("unlimited is a present null: %v", rows[1])
	}
	text, _, _ := runBilling(t, map[string]string{"OwnerLimits": limitsResp3072}, "billing", "limits", "hrn:org:acme.com")
	if !strings.Contains(text, "unlimited") || !strings.Contains(text, "20000") {
		t.Errorf("the table shows unlimited for a null limit:\n%s", text)
	}
	empty, _, _ := runBilling(t, map[string]string{"OwnerLimits": `{"data":{"ownerLimits":[]}}`}, "billing", "limits", "hrn:org:acme.com", "--json")
	if strings.TrimSpace(empty) != "[]" {
		t.Errorf("an empty list is [], not null: %q", empty)
	}
}

func setArgs3072(extra ...string) []string {
	return append([]string{"billing", "limits", "set", "hrn:org:acme.com", "--source-type", "t", "--source-id", "i", "--note", "n", "--yes", "--json"}, extra...)
}

// 8: limits set sends the parsed list, unlimited as null; replace and
// onlySourceType only when given.
func TestBillingLimitsSet3072(t *testing.T) {
	resp := map[string]string{"SetOwnerLimits": `{"data":{"setOwnerLimits":[{"key":"nodes","limit":"20000","used":"3"}]}}`}
	_, captured, err := runBilling(t, resp, setArgs3072("--limit", "nodes=20000", "--limit", "apps=unlimited")...)
	if err != nil {
		t.Fatal(err)
	}
	in := billingInputOf(t, captured, "SetOwnerLimits")
	limits, _ := in["limits"].([]any)
	if len(limits) != 2 {
		t.Fatalf("limits = %v", in["limits"])
	}
	first, second := limits[0].(map[string]any), limits[1].(map[string]any)
	if first["key"] != "nodes" || first["limit"] != "20000" {
		t.Errorf("first = %v", first)
	}
	if v, present := second["limit"]; second["key"] != "apps" || !present || v != nil {
		t.Errorf("unlimited must be an explicit null, not omitted and not the word: %v", second)
	}
	if in["note"] != "n" || in["sourceType"] != "t" || in["sourceId"] != "i" || in["orgRef"] != "hrn:org:acme.com" {
		t.Errorf("input = %v", in)
	}
	for _, k := range []string{"replace", "onlySourceType", "userRef"} {
		if _, sent := in[k]; sent {
			t.Errorf("%s must be omitted unless given: %v", k, in)
		}
	}
	_, captured, err = runBilling(t, resp, setArgs3072("--limit", "nodes=1", "--replace", "--only-source-type", "manual")...)
	if err != nil {
		t.Fatal(err)
	}
	in = billingInputOf(t, captured, "SetOwnerLimits")
	if in["replace"] != true || in["onlySourceType"] != "manual" {
		t.Errorf("replace and onlySourceType must be sent when given: %v", in)
	}
}

// 9: limits set refusals exit 2 and send nothing.
func TestBillingLimitsSetRefusals3072(t *testing.T) {
	for name, args := range map[string][]string{
		"no --limit":       {"billing", "limits", "set", "acme", "--source-type", "t", "--source-id", "i", "--note", "n", "--yes"},
		"no --note":        {"billing", "limits", "set", "acme", "--limit", "nodes=1", "--source-type", "t", "--source-id", "i", "--yes"},
		"no --source-type": {"billing", "limits", "set", "acme", "--limit", "nodes=1", "--source-id", "i", "--note", "n", "--yes"},
		"no --source-id":   {"billing", "limits", "set", "acme", "--limit", "nodes=1", "--source-type", "t", "--note", "n", "--yes"},
		"no =":             setArgs3072("--limit", "nodes"),
		"empty key":        setArgs3072("--limit", "=5"),
		"empty value":      setArgs3072("--limit", "nodes="),
		"duplicate key":    setArgs3072("--limit", "nodes=1", "--limit", "nodes=2"),
		"no --yes":         {"billing", "limits", "set", "acme", "--limit", "nodes=1", "--source-type", "t", "--source-id", "i", "--note", "n"},
	} {
		t.Run(name, func(t *testing.T) {
			_, captured, err := runBilling(t, map[string]string{}, args...)
			if got := exitCodeFor(err); got != exitcode.Usage {
				t.Errorf("want exit 2, got %d (%v)", got, err)
			}
			if len(captured) != 0 {
				t.Errorf("nothing may be sent: %v", captured)
			}
		})
	}
}

// 10: limits clear: --key is optional; without it every key is cleared, the keys
// argument is omitted, and the command asks first.
func TestBillingLimitsClear3072(t *testing.T) {
	resp := map[string]string{"ClearOwnerLimits": `{"data":{"clearOwnerLimits":[]}}`}
	base := []string{"billing", "limits", "clear", "hrn:org:acme.com", "--source-type", "t", "--source-id", "i", "--note", "n", "--json"}
	_, captured, err := runBilling(t, resp, append(append([]string{}, base...), "--yes")...)
	if err != nil {
		t.Fatal(err)
	}
	in := billingInputOf(t, captured, "ClearOwnerLimits")
	if _, sent := in["keys"]; sent {
		t.Errorf("keys must be omitted to clear every key: %v", in)
	}
	_, captured, err = runBilling(t, resp, append(append([]string{}, base...), "--yes", "--key", "nodes", "--key", "apps", "--only-source-type", "manual")...)
	if err != nil {
		t.Fatal(err)
	}
	in = billingInputOf(t, captured, "ClearOwnerLimits")
	if keys, _ := in["keys"].([]any); len(keys) != 2 || in["onlySourceType"] != "manual" {
		t.Errorf("input = %v", in)
	}
	_, captured, err = runBilling(t, resp, base...)
	if got := exitCodeFor(err); got != exitcode.Usage || len(captured) != 0 {
		t.Errorf("without --yes and without a terminal: exit 2, nothing sent; got %d, %v", got, captured)
	}
}

func refusal3072(code, extensions string) string {
	return `{"data":null,"errors":[{"message":"refused","extensions":{"code":"` + code + `"` + extensions + `}}]}`
}

// 8-11: OWNER_LIMIT_REACHED exits 5 and its --json failure carries the server code
// and the extensions exactly as sent.
func TestOwnerLimitReachedIsAConflictWithItsExtensions3072(t *testing.T) {
	full := `,"limitKey":"memories","limit":"5","ownerType":"ORGANIZATION","callerCanManageOwner":true,"ownerId":"org1"`
	partial := `,"limitKey":"memories","ownerType":"USER","callerCanManageOwner":false`
	for name, c := range map[string]struct {
		op   string
		args []string
		ext  string
		want map[string]any
	}{
		"memory set, all fields": {"CreateMemory", []string{"memory", "set", "--org", "acme.com", "--name", "KB"}, full,
			map[string]any{"limitKey": "memories", "limit": "5", "ownerType": "ORGANIZATION", "callerCanManageOwner": true, "ownerId": "org1"}},
		"memory set, a caller who cannot manage the owner": {"CreateMemory", []string{"memory", "set", "--org", "acme.com", "--name", "KB"}, partial,
			map[string]any{"limitKey": "memories", "ownerType": "USER", "callerCanManageOwner": false}},
		"agent clone keeps the fields": {"CloneAgent", []string{"agent", "clone", "src", "--to", "tgt"}, full,
			map[string]any{"limitKey": "agents", "limit": "5", "ownerType": "ORGANIZATION", "callerCanManageOwner": true, "ownerId": "org1"}},
	} {
		t.Run(name, func(t *testing.T) {
			ext := c.ext
			if strings.HasPrefix(name, "agent clone") {
				ext = strings.Replace(ext, `"memories"`, `"agents"`, 1)
			}
			gql, _ := captureGraphQL(t, map[string]string{c.op: refusal3072("OWNER_LIMIT_REACHED", ext)})
			f, out := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs(append(append([]string{}, c.args...), "--json", "--server", gql.URL))
			err := root.Execute()
			if err == nil {
				t.Fatal("a refusal must fail")
			}
			if got := renderError(f, err); got != exitcode.Conflict {
				t.Errorf("exit %d, want 5 (was 1)", got)
			}
			var env struct {
				Error struct {
					ServerCode string         `json:"serverCode"`
					Extensions map[string]any `json:"extensions"`
				} `json:"error"`
			}
			if e := json.Unmarshal([]byte(out.String()), &env); e != nil {
				t.Fatalf("%v\n%s", e, out.String())
			}
			if env.Error.ServerCode != "OWNER_LIMIT_REACHED" {
				t.Errorf("serverCode = %q", env.Error.ServerCode)
			}
			for k, v := range c.want {
				if env.Error.Extensions[k] != v {
					t.Errorf("extensions.%s = %v, want %v: %s", k, env.Error.Extensions[k], v, out.String())
				}
			}
			for _, k := range []string{"limit", "ownerId"} {
				if _, want := c.want[k]; !want {
					if v, present := env.Error.Extensions[k]; present {
						t.Errorf("extensions.%s must be absent when the server omits it, got %v", k, v)
					}
				}
			}
		})
	}
}

// 11: the begin of an asset upload is refused before any bytes are sent.
func TestOwnerLimitReachedStopsAnAssetUploadBeforeTheBytes3072(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"GetMemory":        assetMemoryResp,
		"BeginAssetUpload": refusal3072("OWNER_LIMIT_REACHED", `,"limitKey":"assetBytes","ownerType":"ORGANIZATION","callerCanManageOwner":false`),
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"asset", "upload", writeTempFile(t, "notes.txt", "hello"), "-m", "acme.com::kb", "--server", gql.URL})
	err := root.Execute()
	if err == nil {
		t.Fatal("must fail")
	}
	if got := renderError(f, err); got != exitcode.Conflict {
		t.Errorf("exit %d, want 5", got)
	}
	if _, completed := captured["CompleteAssetUpload"]; completed {
		t.Error("nothing may complete after the begin was refused")
	}
}

// 12 (a guard pin, green on main through the _TOO_LARGE suffix rule): a node
// write over the size ceiling exits 2; its --json failure also carries the code
// and maxBytes (the gate's ruling 5).
func TestNodeContentTooLargeIsUsageWithMaxBytes3072(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{"CreateNode": refusal3072("NODE_CONTENT_TOO_LARGE", `,"maxBytes":1048576`)})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "create", "-m", "acme.com::kb", "--loc", "x", "--name", "X", "--content", "c", "--json", "--server", gql.URL})
	err := root.Execute()
	if err == nil {
		t.Fatal("must fail")
	}
	if got := renderError(f, err); got != exitcode.Usage {
		t.Errorf("exit %d, want 2", got)
	}
	var env struct {
		Error struct {
			ServerCode string         `json:"serverCode"`
			Extensions map[string]any `json:"extensions"`
		} `json:"error"`
	}
	_ = json.Unmarshal([]byte(out.String()), &env)
	if env.Error.ServerCode != "NODE_CONTENT_TOO_LARGE" || env.Error.Extensions["maxBytes"] != float64(1048576) {
		t.Errorf("serverCode and maxBytes must reach the failure: %s", out.String())
	}
}
