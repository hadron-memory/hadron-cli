package accountusage

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Fixtures only: no real quota response, account or network (cli#3312).

var glmNow = time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)

func glmAccount(t *testing.T, body string) Account {
	t.Helper()
	dir := t.TempDir()
	if body != "" {
		d := filepath.Join(dir, "hadron-usage")
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, "glm-quota.json"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return Account{Label: "glm-1", ConfigDir: dir, Family: "glm"}
}

func glmRecord(t *testing.T, a Account) Record {
	t.Helper()
	ps, err := Prepare(&Config{Accounts: []Account{a}})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	return BuildRecord(ps[0], "host-1", glmNow, Previous{})
}

const glmOK = `{"observedAt":1795000000,"limits":[
 {"type":"TOKENS_LIMIT","unit":3,"number":5,"percentage":42,"nextResetTime":1795003600000}]}`

// Red on main: a glm account was always no-local-source.
func TestGLMValidFileIsOK(t *testing.T) {
	r := glmRecord(t, glmAccount(t, glmOK))
	if r.SourceState != "ok" {
		t.Fatalf("sourceState = %q, want ok", r.SourceState)
	}
	if r.PrimaryPct == nil || *r.PrimaryPct != 42 || *r.PrimaryMinutes != 300 || *r.PrimaryResetsAt != "2026-11-18T12:06:40Z" {
		t.Fatalf("primary = %+v", r)
	}
	if r.SecondaryPct != nil || r.SecondaryMinutes != nil || r.SecondaryResetsAt != nil {
		t.Fatalf("no second confirmed window, secondary must be absent: %+v", r)
	}
	if r.FiveHourPct != nil || r.SevenDayPct != nil {
		t.Fatal("a glm record carries own-length windows, not the Claude 5h/7d fields")
	}
}

func TestGLMMissingFileIsNoLocalSource(t *testing.T) {
	r := glmRecord(t, glmAccount(t, ""))
	if r.SourceState != "no-local-source" || r.PrimaryPct != nil {
		t.Fatalf("got %+v", r)
	}
}

func TestGLMOnlyUnconfirmedCodesIsNoReadingYet(t *testing.T) {
	// (6,1) is deliberately unconfirmed (weekly vs monthly): dropped, not guessed.
	body := `{"observedAt":1795000000,"limits":[
	 {"type":"TOKENS_LIMIT","unit":6,"number":1,"percentage":10,"nextResetTime":1795003600000},
	 {"type":"TIME_LIMIT","unit":3,"number":5,"percentage":90,"nextResetTime":1795003600000},
	 {"type":"SOMETHING_NEW","unit":3,"number":5,"percentage":90,"nextResetTime":1795003600000},
	 {"type":"TOKENS_LIMIT","unit":9,"number":9,"percentage":5,"nextResetTime":1795003600000}]}`
	r := glmRecord(t, glmAccount(t, body))
	if r.SourceState != "no-reading-yet" {
		t.Fatalf("sourceState = %q", r.SourceState)
	}
	if r.PrimaryPct != nil || r.SecondaryPct != nil {
		t.Fatalf("an unconfirmed code must never become a share: %+v", r)
	}
	for _, empty := range []string{`{"observedAt":1795000000,"limits":[]}`, `{"observedAt":1795000000}`} {
		if r := glmRecord(t, glmAccount(t, empty)); r.SourceState != "no-reading-yet" {
			t.Fatalf("%s -> %q", empty, r.SourceState)
		}
	}
}

func TestGLMCreditLimitParsed(t *testing.T) {
	body := `{"observedAt":1795000000,"limits":[
	 {"type":"CREDIT_LIMIT","unit":3,"number":5,"percentage":17,"nextResetTime":1795003600000}]}`
	r := glmRecord(t, glmAccount(t, body))
	if r.SourceState != "ok" || r.PrimaryPct == nil || *r.PrimaryPct != 17 {
		t.Fatalf("CREDIT_LIMIT not parsed: %+v", r)
	}
}

func TestGLMSameLengthKeepsTheMoreUsed(t *testing.T) {
	body := `{"observedAt":1795000000,"limits":[
	 {"type":"TOKENS_LIMIT","unit":3,"number":5,"percentage":10,"nextResetTime":1795003600000},
	 {"type":"CREDIT_LIMIT","unit":3,"number":5,"percentage":60,"nextResetTime":1795003600000}]}`
	if r := glmRecord(t, glmAccount(t, body)); *r.PrimaryPct != 60 || r.SecondaryPct != nil {
		t.Fatalf("got %+v", r)
	}
}

func TestGLMZeroIsReportedNotAbsent(t *testing.T) {
	body := `{"observedAt":1795000000,"limits":[
	 {"type":"TOKENS_LIMIT","unit":3,"number":5,"percentage":0,"nextResetTime":1795003600000}]}`
	if r := glmRecord(t, glmAccount(t, body)); r.PrimaryPct == nil || *r.PrimaryPct != 0 {
		t.Fatalf("a reported 0%% must stay 0: %+v", r)
	}
}

func TestGLMRefusals(t *testing.T) {
	cases := map[string]string{
		"pct over 100":      `{"observedAt":1795000000,"limits":[{"type":"TOKENS_LIMIT","unit":3,"number":5,"percentage":101,"nextResetTime":1795003600000}]}`,
		"pct negative":      `{"observedAt":1795000000,"limits":[{"type":"TOKENS_LIMIT","unit":3,"number":5,"percentage":-1,"nextResetTime":1795003600000}]}`,
		"pct missing":       `{"observedAt":1795000000,"limits":[{"type":"TOKENS_LIMIT","unit":3,"number":5,"nextResetTime":1795003600000}]}`,
		"reset missing":     `{"observedAt":1795000000,"limits":[{"type":"TOKENS_LIMIT","unit":3,"number":5,"percentage":5}]}`,
		"reset in seconds?": `{"observedAt":1795000000,"limits":[{"type":"TOKENS_LIMIT","unit":3,"number":5,"percentage":5,"nextResetTime":-5}]}`,
		"no observedAt":     `{"limits":[]}`,
		"bad observedAt":    `{"observedAt":-1,"limits":[]}`,
		"not json":          `not json SECRET-TOKEN-123`,
		"wrong shape":       `{"observedAt":1795000000,"limits":"x"}`,
	}
	for name, body := range cases {
		_, err := Prepare(&Config{Accounts: []Account{glmAccount(t, body)}})
		var ve *ValidationError
		if !errors.As(err, &ve) {
			t.Errorf("%s: want a ValidationError, got %v", name, err)
			continue
		}
		if strings.Contains(err.Error(), "SECRET-TOKEN") {
			t.Errorf("%s: error echoes file contents: %v", name, err)
		}
	}
}

func TestGLMTooManyLimitsAndSizeRefused(t *testing.T) {
	many := `{"observedAt":1795000000,"limits":[` + strings.Repeat(`{"type":"X"},`, maxGLMLimits) + `{"type":"X"}]}`
	if _, err := ReadGLM(glmAccount(t, many)); err == nil {
		t.Error("too many limits accepted")
	}
	big := `{"observedAt":1795000000,"pad":"` + strings.Repeat("a", maxGLMQuotaBytes) + `","limits":[]}`
	if _, err := ReadGLM(glmAccount(t, big)); err == nil {
		t.Error("oversized file accepted")
	}
}

func TestGLMSymlinkRefused(t *testing.T) {
	a := glmAccount(t, "")
	d := filepath.Join(a.ConfigDir, "hadron-usage")
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(t.TempDir(), "creds")
	if err := os.WriteFile(secret, []byte(glmOK), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(d, "glm-quota.json")); err != nil {
		t.Skip("no symlinks")
	}
	if _, err := ReadGLM(a); err == nil {
		t.Fatal("a symlinked quota file must be refused")
	}
}

// The reader makes no network call and reads no env/key file: collect holds no
// key (cli#3312 build-to). Pinned on the source: the glm reader imports no
// net/http, net, os/exec and never calls os.Getenv/ReadFile/Environ.
func TestGLMReaderHoldsNoKeyAndNoNetwork(t *testing.T) {
	src, err := os.ReadFile("glm.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, banned := range []string{`"net"`, `"net/http"`, `"os/exec"`, "os.Getenv", "os.LookupEnv", "os.Environ", "os.ReadFile", "ReadDir", "UserHomeDir", ".env", "ANTHROPIC_"} {
		for _, line := range strings.Split(string(src), "\n") {
			if t := strings.TrimSpace(line); strings.HasPrefix(t, "//") {
				continue
			}
			if strings.Contains(line, banned) {
				t.Errorf("glm.go contains %q: the glm reader must hold no key and make no network call", banned)
			}
		}
	}
}
