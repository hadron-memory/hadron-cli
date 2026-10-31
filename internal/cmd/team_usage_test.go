package cmd

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// usageServer answers CreateObject / FindObjects and records every request, so
// a test can assert how many records were written and with what fields.
type usageServer struct {
	mu       sync.Mutex
	creates  []map[string]any
	finds    []map[string]any
	findResp string
}

func newUsageServer(t *testing.T, findResp string) (*httptest.Server, *usageServer) {
	t.Helper()
	us := &usageServer{findResp: findResp}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`{"capabilities":[]}`))
			return
		}
		raw, _ := io.ReadAll(r.Body)
		var body struct {
			OperationName string         `json:"operationName"`
			Variables     map[string]any `json:"variables"`
		}
		_ = json.Unmarshal(raw, &body)
		us.mu.Lock()
		defer us.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch body.OperationName {
		case "CreateObject":
			us.creates = append(us.creates, body.Variables)
			_, _ = w.Write([]byte(`{"data":{"createObject":{"id":"obj-` + string(rune('0'+len(us.creates))) + `"}}}`))
		case "FindObjects":
			us.finds = append(us.finds, body.Variables)
			_, _ = w.Write([]byte(us.findResp))
		default:
			t.Errorf("unexpected operation %q", body.OperationName)
			_, _ = w.Write([]byte(`{"errors":[{"message":"unexpected"}]}`))
		}
	}))
	t.Cleanup(srv.Close)
	return srv, us
}

const noObjects = `{"data":{"findObjects":{"objects":[],"total":0}}}`

// usageFixture lays out two accounts: A with both windows and a ccusage file,
// B with only a seven-day window and no ccusage; returns the config path.
func usageFixture(t *testing.T) (cfg string, dirA, dirB string) {
	t.Helper()
	root := t.TempDir()
	dirA, dirB = filepath.Join(root, "a"), filepath.Join(root, "b")
	for _, d := range []string{dirA, dirB} {
		if err := os.MkdirAll(filepath.Join(d, "hadron-usage"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(p, s string) {
		t.Helper()
		if err := os.WriteFile(p, []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// A carries extra keys a status line might pass through: they must never
	// reach a record.
	write(filepath.Join(dirA, "hadron-usage", "rate-limits.json"), `{"observedAt":1793476800,"five_hour":{"used_percentage":42.5,"resets_at":1793481600},"seven_day":{"used_percentage":10,"resets_at":1793966400},"email":"leak@example.com","token":"SECRET-TOKEN"}`)
	write(filepath.Join(dirB, "hadron-usage", "rate-limits.json"), `{"observedAt":1793477100,"seven_day":{"used_percentage":77,"resets_at":1793966400}}`)
	ccu := filepath.Join(root, "ccusage-a.json")
	write(ccu, `{"blocks":[
	 {"startTime":"2026-10-31T10:00:00.000Z","isGap":false,"totalTokens":1000,"costUSD":1.5},
	 {"startTime":"2026-10-31T14:00:00.000Z","isGap":true,"totalTokens":0,"costUSD":0},
	 {"startTime":"2026-10-31T16:00:00.000Z","isGap":false,"totalTokens":2000,"costUSD":2.25}]}`)
	cfg = filepath.Join(root, "usage.yaml")
	write(cfg, "memory: hrn:mem:acme.com:team\nhost: orca-1\naccounts:\n"+
		"  - label: acct-a\n    configDir: "+dirA+"\n    family: claude\n    ccusageFile: "+ccu+"\n    workers: [Ada, Joshua]\n"+
		"  - label: acct-b\n    configDir: "+dirB+"\n")
	return cfg, dirA, dirB
}

func runUsage(t *testing.T, srvURL string, args ...string) (string, error) {
	t.Helper()
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append([]string{"team", "usage"}, append(args, "--server", srvURL)...))
	err := root.Execute()
	return out.String(), err
}

func fieldsOf(t *testing.T, c map[string]any) map[string]any {
	t.Helper()
	raw, ok := c["fields"]
	if !ok {
		t.Fatalf("no fields in %v", c)
	}
	m, ok := raw.(map[string]any)
	if !ok {
		t.Fatalf("fields not an object: %T", raw)
	}
	return m
}

// Acceptance 1: one record per account; an absent window is absent, not 0.
func TestTeamUsageCollectWritesOneRecordPerAccount(t *testing.T) {
	cfg, _, _ := usageFixture(t)
	// The previous record (11:00Z) checkpointed the 10:00 block at 400 tokens /
	// $0.50: it grew by 600 / $1.00, and the 16:00 block is new (2000 / $2.25).
	srv, us := newUsageServer(t, `{"data":{"findObjects":{"objects":[{"observedAt":"2026-10-31T11:00:00Z","ccusageBlocks":[{"start":"2026-10-31T10:00:00Z","tokens":400,"costUsd":0.5}]}],"total":1}}}`)
	if _, err := runUsage(t, srv.URL, "collect", "--config", cfg, "--json"); err != nil {
		t.Fatal(err)
	}
	if len(us.creates) != 2 {
		t.Fatalf("want 2 records, got %d", len(us.creates))
	}
	a, b := fieldsOf(t, us.creates[0]), fieldsOf(t, us.creates[1])
	if us.creates[0]["objectType"] != "account-usage" || us.creates[0]["memoryRef"] != "hrn:mem:acme.com:team" {
		t.Errorf("collection/memory: %v", us.creates[0])
	}
	if a["label"] != "acct-a" || a["fiveHourPct"] != 42.5 || a["sevenDayPct"] != float64(10) || a["fiveHourResetsAt"] != "2026-10-31T21:20:00Z" {
		t.Errorf("acct-a: %v", a)
	}
	if a["tokensSinceLast"] != float64(2600) || a["estCostUsdSinceLast"] != 3.25 {
		t.Errorf("since-last must be the growth of the totals since the previous checkpoint: %v", a)
	}
	if a["workersConfigured"] != true || len(a["workers"].([]any)) != 2 {
		t.Errorf("workers: %v", a)
	}
	for _, k := range []string{"fiveHourPct", "fiveHourResetsAt", "tokensSinceLast", "estCostUsdSinceLast"} {
		if _, has := b[k]; has {
			t.Errorf("acct-b must not carry %s (absent, never 0): %v", k, b)
		}
	}
	if b["sevenDayPct"] != float64(77) || b["workersConfigured"] != false || len(b["workers"].([]any)) != 0 {
		t.Errorf("acct-b: %v", b)
	}
}

// Acceptance 3: nothing but the documented fields reaches a record.
func TestTeamUsageRecordsCarryNoPassThroughData(t *testing.T) {
	cfg, _, _ := usageFixture(t)
	srv, us := newUsageServer(t, noObjects)
	if _, err := runUsage(t, srv.URL, "collect", "--config", cfg); err != nil {
		t.Fatal(err)
	}
	for _, c := range us.creates {
		raw, _ := json.Marshal(c)
		for _, bad := range []string{"leak@example.com", "SECRET-TOKEN", "email", "accessToken", "refreshToken", "oauth"} {
			if strings.Contains(string(raw), bad) {
				t.Errorf("record leaks %q: %s", bad, raw)
			}
		}
	}
}

// The record shape is pinned: the portal reads it without the CLI.
func TestTeamUsageRecordGolden(t *testing.T) {
	cfg, _, _ := usageFixture(t)
	srv, us := newUsageServer(t, noObjects)
	if _, err := runUsage(t, srv.URL, "collect", "--config", cfg); err != nil {
		t.Fatal(err)
	}
	got, _ := json.Marshal(fieldsOf(t, us.creates[0]))
	want := `{"ccusageBlocks":[{"costUsd":2.25,"start":"2026-10-31T16:00:00Z","tokens":2000}],"family":"claude","fiveHourPct":42.5,"fiveHourResetsAt":"2026-10-31T21:20:00Z","host":"orca-1","label":"acct-a","observedAt":"2026-10-31T20:00:00Z","schemaVersion":1,"sevenDayPct":10,"sevenDayResetsAt":"2026-11-06T12:00:00Z","workers":["Ada","Joshua"],"workersConfigured":true}`
	if string(got) != want {
		t.Errorf("record shape changed:\n got %s\nwant %s", got, want)
	}
}

// Acceptance 8: fail closed, exit 2, no record written for ANY account (the
// first account is valid in every case: it must not be written either).
func TestTeamUsageCollectFailsClosed(t *testing.T) {
	cases := map[string]func(t *testing.T, cfg, dirA, dirB string){
		"missing config":              func(t *testing.T, cfg, _, _ string) { _ = os.Remove(cfg) },
		"malformed config":            func(t *testing.T, cfg, _, _ string) { _ = os.WriteFile(cfg, []byte("accounts: [unterminated"), 0o600) },
		"unknown config key":          func(t *testing.T, cfg, _, _ string) { appendFile(t, cfg, "bogus: 1\n") },
		"trailing malformed document": func(t *testing.T, cfg, _, _ string) { appendFile(t, cfg, "---\naccounts: [\n") },
		"trailing second document":    func(t *testing.T, cfg, _, _ string) { appendFile(t, cfg, "---\nmemory: x\n") },
		"label with @": func(t *testing.T, cfg, _, _ string) {
			b, _ := os.ReadFile(cfg)
			_ = os.WriteFile(cfg, []byte(strings.Replace(string(b), "acct-b", "private-person@example.test", 1)), 0o600)
		},
		"malformed snapshot": func(t *testing.T, _, _, dirB string) {
			_ = os.WriteFile(filepath.Join(dirB, "hadron-usage", "rate-limits.json"), []byte(`{"observedAt": SENTINEL-SECRET`), 0o600)
		},
		"percentage out of range": func(t *testing.T, _, _, dirB string) {
			_ = os.WriteFile(filepath.Join(dirB, "hadron-usage", "rate-limits.json"), []byte(`{"observedAt":1793477100,"five_hour":{"used_percentage":101,"resets_at":1793481600}}`), 0o600)
		},
		"window missing resets_at": func(t *testing.T, _, _, dirB string) {
			_ = os.WriteFile(filepath.Join(dirB, "hadron-usage", "rate-limits.json"), []byte(`{"observedAt":1793477100,"five_hour":{"used_percentage":5}}`), 0o600)
		},
		"unreadable snapshot": func(t *testing.T, _, _, dirB string) {
			p := filepath.Join(dirB, "hadron-usage", "rate-limits.json")
			_ = os.Remove(p)
			_ = os.Mkdir(p, 0o755) // a directory where the file should be
		},
		"symlinked snapshot": func(t *testing.T, _, _, dirB string) {
			p := filepath.Join(dirB, "hadron-usage", "rate-limits.json")
			_ = os.Remove(p)
			creds := filepath.Join(dirB, ".credentials.json")
			_ = os.WriteFile(creds, []byte(`{"observedAt":1793477100,"seven_day":{"used_percentage":1,"resets_at":1793966400},"accessToken":"SENTINEL-SECRET"}`), 0o600)
			if err := os.Symlink(creds, p); err != nil {
				t.Skip("no symlinks")
			}
		},
		"malformed ccusage": func(t *testing.T, cfg, dirA, _ string) {
			_ = os.WriteFile(filepath.Join(filepath.Dir(dirA), "ccusage-a.json"), []byte(`{"blocks": SENTINEL-SECRET`), 0o600)
		},
		"symlinked ccusage": func(t *testing.T, _, dirA, _ string) {
			p := filepath.Join(filepath.Dir(dirA), "ccusage-a.json")
			real := filepath.Join(filepath.Dir(dirA), "ccusage-real.json")
			if err := os.Rename(p, real); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(real, p); err != nil {
				t.Skip("no symlinks")
			}
		},
		"oversized snapshot": func(t *testing.T, _, _, dirB string) {
			pad := strings.Repeat(" ", (1<<20)+1)
			_ = os.WriteFile(filepath.Join(dirB, "hadron-usage", "rate-limits.json"), []byte(`{"observedAt":1793477100}`+pad), 0o600)
		},
		"oversized ccusage": func(t *testing.T, _, dirA, _ string) {
			pad := strings.Repeat(" ", (16<<20)+1)
			_ = os.WriteFile(filepath.Join(filepath.Dir(dirA), "ccusage-a.json"), []byte(`{"blocks":[]}`+pad), 0o600)
		},
		"worker name with @": func(t *testing.T, cfg, _, _ string) {
			b, _ := os.ReadFile(cfg)
			_ = os.WriteFile(cfg, []byte(strings.Replace(string(b), "[Ada, Joshua]", "[Ada, a@example.test]", 1)), 0o600)
		},
		"unreadable ccusage": func(t *testing.T, cfg, dirA, _ string) {
			_ = os.Remove(filepath.Join(filepath.Dir(dirA), "ccusage-a.json"))
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			cfg, dirA, dirB := usageFixture(t)
			mutate(t, cfg, dirA, dirB)
			srv, us := newUsageServer(t, noObjects)
			f, out := testFactory(t)
			errOut := &strings.Builder{}
			f.IOStreams.ErrOut = errOut
			root := NewRootCmd(f)
			root.SetArgs([]string{"team", "usage", "collect", "--config", cfg, "--server", srv.URL})
			err := root.Execute()
			if got := exitCodeFor(err); got != 2 {
				t.Fatalf("exit=%d want 2 (err=%v)", got, err)
			}
			if len(us.creates) != 0 {
				t.Fatalf("a record was written: %v", us.creates)
			}
			all := out.String() + errOut.String()
			if err != nil {
				all += err.Error()
			}
			if strings.Contains(all, "SENTINEL-SECRET") || strings.Contains(all, "private-person") || strings.Contains(all, "example.test") {
				t.Errorf("file contents or a rejected label echoed: %s", all)
			}
		})
	}
}

func appendFile(t *testing.T, p, s string) {
	t.Helper()
	f, err := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	_, _ = f.WriteString(s)
}

// A snapshot that does not exist yet is no data, not a failure.
func TestTeamUsageMissingSnapshotIsNoData(t *testing.T) {
	cfg, _, dirB := usageFixture(t)
	_ = os.Remove(filepath.Join(dirB, "hadron-usage", "rate-limits.json"))
	srv, us := newUsageServer(t, noObjects)
	if _, err := runUsage(t, srv.URL, "collect", "--config", cfg); err != nil {
		t.Fatal(err)
	}
	if len(us.creates) != 2 {
		t.Fatalf("want 2 records, got %d", len(us.creates))
	}
	b := fieldsOf(t, us.creates[1])
	for _, k := range []string{"fiveHourPct", "sevenDayPct", "fiveHourResetsAt", "sevenDayResetsAt"} {
		if _, has := b[k]; has {
			t.Errorf("no data must leave %s absent: %v", k, b)
		}
	}
}

// The reader opens only the two files it was given: no subprocess, ever.
func TestAccountUsagePackageRunsNoSubprocess(t *testing.T) {
	entries, err := os.ReadDir("../accountusage")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), "_test.go") || !strings.HasSuffix(e.Name(), ".go") {
			continue
		}
		b, _ := os.ReadFile(filepath.Join("../accountusage", e.Name()))
		for _, banned := range []string{`"os/exec"`, "exec.Command", "syscall.Exec", `"net/http"`} {
			if strings.Contains(string(b), banned) {
				t.Errorf("%s uses %s", e.Name(), banned)
			}
		}
	}
	// collect itself must not exec either: it runs with an empty PATH.
	cfg, _, _ := usageFixture(t)
	srv, us := newUsageServer(t, noObjects)
	t.Setenv("PATH", "")
	if _, err := runUsage(t, srv.URL, "collect", "--config", cfg); err != nil {
		t.Fatal(err)
	}
	if len(us.creates) != 2 {
		t.Fatalf("collect needs no external program: %d records", len(us.creates))
	}
}

// Acceptance 2: show computes current use, resets and burn rate from history,
// with a stable []-not-null DTO.
func TestTeamUsageShowComputesBurnRate(t *testing.T) {
	// acct-a: three points in one 5h window (resets 21:20Z): 10% -> 20% -> 40%
	// over 2h, so 15 %/h; a point from the previous window must not count.
	objs := []string{
		`{"label":"acct-a","family":"claude","host":"h","observedAt":"2090-10-31T20:00:00Z","fiveHourPct":40,"fiveHourResetsAt":"2090-10-31T21:20:00Z","workers":[],"workersConfigured":false,"schemaVersion":1}`,
		`{"label":"acct-a","family":"claude","host":"h","observedAt":"2090-10-31T19:00:00Z","fiveHourPct":20,"fiveHourResetsAt":"2090-10-31T21:20:00Z","workers":[],"workersConfigured":false,"schemaVersion":1}`,
		`{"label":"acct-a","family":"claude","host":"h","observedAt":"2090-10-31T18:00:00Z","fiveHourPct":10,"fiveHourResetsAt":"2090-10-31T21:20:00Z","workers":[],"workersConfigured":false,"schemaVersion":1}`,
		`{"label":"acct-a","family":"claude","host":"h","observedAt":"2090-10-31T16:00:00Z","fiveHourPct":99,"fiveHourResetsAt":"2090-10-31T16:30:00Z","workers":[],"workersConfigured":false,"schemaVersion":1}`,
		`{"label":"acct-b","family":"claude","host":"h","observedAt":"2090-10-31T20:00:00Z","sevenDayPct":5,"sevenDayResetsAt":"2090-11-06T00:00:00Z","workers":["Ada"],"workersConfigured":true,"schemaVersion":1}`,
		`"not a record"`,
	}
	srv, _ := newUsageServer(t, `{"data":{"findObjects":{"objects":[`+strings.Join(objs, ",")+`],"total":6}}}`)
	out, err := runUsage(t, srv.URL, "show", "-m", "hrn:mem:acme.com:team", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var dto struct {
		Accounts []struct {
			Label    string `json:"label"`
			Points   int    `json:"points"`
			FiveHour struct {
				UsedPct        *float64 `json:"usedPct"`
				BurnPctPerHour *float64 `json:"burnPctPerHour"`
				ExhaustsAt     *string  `json:"exhaustsAt"`
				Before         *bool    `json:"exhaustsBeforeReset"`
			} `json:"fiveHour"`
			SevenDay struct {
				UsedPct        *float64 `json:"usedPct"`
				BurnPctPerHour *float64 `json:"burnPctPerHour"`
			} `json:"sevenDay"`
			Workers []string `json:"workers"`
		} `json:"accounts"`
	}
	if err := json.Unmarshal([]byte(out), &dto); err != nil {
		t.Fatalf("%v: %q", err, out)
	}
	if len(dto.Accounts) != 2 || dto.Accounts[0].Label != "acct-a" || dto.Accounts[1].Label != "acct-b" {
		t.Fatalf("accounts: %s", out)
	}
	a := dto.Accounts[0].FiveHour
	if a.UsedPct == nil || *a.UsedPct != 40 || a.BurnPctPerHour == nil || *a.BurnPctPerHour != 15 {
		t.Errorf("acct-a five_hour: %+v", a)
	}
	// 60% left at 15 %/h = 4h after 20:00 = 00:00Z, after the 21:20 reset.
	if a.ExhaustsAt == nil || *a.ExhaustsAt != "2090-11-01T00:00:00Z" || a.Before == nil || *a.Before {
		t.Errorf("projection: %+v", a)
	}
	if dto.Accounts[1].SevenDay.UsedPct == nil || dto.Accounts[1].SevenDay.BurnPctPerHour != nil {
		t.Errorf("one point has a use but no rate: %+v", dto.Accounts[1].SevenDay)
	}
	if dto.Accounts[0].Points != 4 {
		t.Errorf("unparseable record must be skipped, points=%d", dto.Accounts[0].Points)
	}
}

func TestTeamUsageShowEmptyIsAnEmptyList(t *testing.T) {
	srv, _ := newUsageServer(t, noObjects)
	out, err := runUsage(t, srv.URL, "show", "-m", "hrn:mem:acme.com:team", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"accounts": []`) {
		t.Errorf("want accounts [] not null: %s", out)
	}
}

// Since-last is the growth of the recorded blocks: usage inside a block that
// was already running at the previous record counts, blocks evicted from a
// rolling ccusage file do not skew it, and a checkpoint that cannot be
// compared (a block went down, or none was recorded) leaves it absent.
func TestTeamUsageSinceLastCountsGrowthInsideARunningBlock(t *testing.T) {
	cfg, _, _ := usageFixture(t)
	collect := func(prev string) map[string]any {
		t.Helper()
		srv, us := newUsageServer(t, `{"data":{"findObjects":{"objects":[`+prev+`],"total":1}}}`)
		if _, err := runUsage(t, srv.URL, "collect", "--config", cfg); err != nil {
			t.Fatal(err)
		}
		return fieldsOf(t, us.creates[0])
	}
	// The 10:00 block was 500 / $0.50 at 11:00 and is 1000 / $1.50 now; the
	// 16:00 block is new: 500 + 2000 tokens, $1.00 + $2.25.
	a := collect(`{"observedAt":"2026-10-31T11:00:00Z","ccusageBlocks":[{"start":"2026-10-31T10:00:00Z","tokens":500,"costUsd":0.5}]}`)
	if a["tokensSinceLast"] != float64(2500) || a["estCostUsdSinceLast"] != 3.25 {
		t.Errorf("growth inside a running block: %v", a)
	}
	// Old blocks (final, so never checkpointed) can vanish from a rolling file
	// without effect: the 10:00 block is unchanged, only the 16:00 block counts.
	a = collect(`{"observedAt":"2026-10-31T11:00:00Z","ccusageBlocks":[{"start":"2026-10-31T10:00:00Z","tokens":1000,"costUsd":1.5}]}`)
	if a["tokensSinceLast"] != float64(2000) || a["estCostUsdSinceLast"] != 2.25 {
		t.Errorf("unchanged checkpointed block plus a new one: %v", a)
	}
	// A checkpointed, still-growing block that is gone from the file has final
	// usage we never saw: the interval is unknown, not smaller.
	a = collect(`{"observedAt":"2026-10-31T11:00:00Z","ccusageBlocks":[{"start":"2026-10-31T09:00:00Z","tokens":600,"costUsd":1},{"start":"2026-10-31T10:00:00Z","tokens":1000,"costUsd":1.5}]}`)
	if _, has := a["tokensSinceLast"]; has {
		t.Errorf("a vanished growing block must leave since-last absent: %v", a)
	}
	// A known block whose totals went down cannot be compared: absent.
	a = collect(`{"observedAt":"2026-10-31T11:00:00Z","ccusageBlocks":[{"start":"2026-10-31T10:00:00Z","tokens":9000,"costUsd":9}]}`)
	if _, has := a["tokensSinceLast"]; has {
		t.Errorf("a shrinking block must leave since-last absent: %v", a)
	}
	// A previous record without a checkpoint: absent, never 0.
	a = collect(`{"observedAt":"2026-10-31T11:00:00Z"}`)
	if _, has := a["tokensSinceLast"]; has {
		t.Errorf("no checkpoint must leave since-last absent: %v", a)
	}
	if blocks, _ := a["ccusageBlocks"].([]any); len(blocks) != 1 {
		t.Errorf("the checkpoint is still recorded: %v", a)
	}
}

// The checkpoint lookup is per host AND family AND label, like show.
func TestTeamUsageCheckpointLookupIncludesFamily(t *testing.T) {
	cfg, _, _ := usageFixture(t)
	srv, us := newUsageServer(t, noObjects)
	if _, err := runUsage(t, srv.URL, "collect", "--config", cfg); err != nil {
		t.Fatal(err)
	}
	if len(us.finds) == 0 {
		t.Fatal("no checkpoint lookup")
	}
	raw, _ := json.Marshal(us.finds[0])
	for _, want := range []string{`"label":"acct-a"`, `"host":"orca-1"`, `"family":"claude"`} {
		if !strings.Contains(strings.ReplaceAll(string(raw), `\"`, `"`), want) {
			t.Errorf("lookup match lacks %s: %s", want, raw)
		}
	}
}

// Labels are host-local: the same label on two hosts is two accounts.
func TestTeamUsageShowKeepsHostsApart(t *testing.T) {
	objs := []string{
		`{"label":"primary","family":"claude","host":"h1","observedAt":"2090-10-31T18:00:00Z","fiveHourPct":10,"fiveHourResetsAt":"2090-10-31T21:20:00Z","workers":[],"workersConfigured":false,"schemaVersion":1}`,
		`{"label":"primary","family":"claude","host":"h2","observedAt":"2090-10-31T19:00:00Z","fiveHourPct":90,"fiveHourResetsAt":"2090-10-31T21:20:00Z","workers":[],"workersConfigured":false,"schemaVersion":1}`,
	}
	srv, _ := newUsageServer(t, `{"data":{"findObjects":{"objects":[`+strings.Join(objs, ",")+`],"total":2}}}`)
	out, err := runUsage(t, srv.URL, "show", "-m", "hrn:mem:acme.com:team", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var dto struct {
		Accounts []struct {
			Host     string `json:"host"`
			Points   int    `json:"points"`
			FiveHour struct {
				BurnPctPerHour *float64 `json:"burnPctPerHour"`
			} `json:"fiveHour"`
		} `json:"accounts"`
	}
	if err := json.Unmarshal([]byte(out), &dto); err != nil {
		t.Fatal(err)
	}
	if len(dto.Accounts) != 2 {
		t.Fatalf("want two accounts, got %s", out)
	}
	for _, a := range dto.Accounts {
		if a.Points != 1 || a.FiveHour.BurnPctPerHour != nil {
			t.Errorf("host %s must not borrow the other host's history: %+v", a.Host, a)
		}
	}
}
