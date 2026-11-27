package accountusage

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The snippet runs for real here against a fake curl: pins the whitelist, the
// 5-minute mtime throttle, fail-closed, and that the token never reaches argv.

func snippetEnv(t *testing.T) (cfg, bin string) {
	t.Helper()
	for _, tool := range []string{"sh", "jq", "find", "sed"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not available", tool)
		}
	}
	root := t.TempDir()
	cfg, bin = filepath.Join(root, "cfg"), filepath.Join(root, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	return cfg, bin
}

// fakeCurl logs argv and stdin to <bin>/calls and answers with <bin>/reply
// (exit 22 like curl -f when the reply file is absent).
func fakeCurl(t *testing.T, bin string) {
	t.Helper()
	script := "#!/bin/sh\necho \"ARGV: $*\" >> \"" + bin + "/calls\"\ncat > \"" + bin + "/stdin\"\n[ -f \"" + bin + "/reply\" ] || exit 22\ncat \"" + bin + "/reply\"\n"
	if err := os.WriteFile(filepath.Join(bin, "curl"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

func runSnippet(t *testing.T, cfg, bin string, env ...string) {
	t.Helper()
	cmd := exec.Command("sh", "-c", GLMSnippet)
	cmd.Env = append([]string{"PATH=" + bin + ":" + os.Getenv("PATH"), "HOME=" + filepath.Dir(cfg), "CLAUDE_CONFIG_DIR=" + cfg}, env...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("snippet: %v\n%s", err, out)
	}
}

func calls(bin string) int {
	b, err := os.ReadFile(filepath.Join(bin, "calls"))
	if err != nil {
		return 0
	}
	return strings.Count(string(b), "ARGV:")
}

const quotaReply = `{"code":200,"success":true,"data":{"planName":"Pro","email":"someone@example.com","limits":[
 {"type":"TOKENS_LIMIT","unit":3,"number":5,"usage":1000,"currentValue":420,"remaining":580,"percentage":42,"nextResetTime":1795003600000,"usageDetails":[{"modelCode":"x"}]}]}}`

func TestGLMSnippetWritesOnlyWhitelistedFields(t *testing.T) {
	cfg, bin := snippetEnv(t)
	fakeCurl(t, bin)
	if err := os.WriteFile(filepath.Join(bin, "reply"), []byte(quotaReply), 0o600); err != nil {
		t.Fatal(err)
	}
	runSnippet(t, cfg, bin, "ANTHROPIC_AUTH_TOKEN=SECRET-TOKEN-123", "ANTHROPIC_BASE_URL=https://api.z.ai/api/anthropic")
	b, err := os.ReadFile(filepath.Join(cfg, "hadron-usage", "glm-quota.json"))
	if err != nil {
		t.Fatal(err)
	}
	got := string(b)
	for _, leaked := range []string{"SECRET-TOKEN", "someone@example.com", "planName", "usage", "currentValue", "remaining", "email"} {
		if strings.Contains(got, leaked) {
			t.Errorf("quota file leaks %q: %s", leaked, got)
		}
	}
	// The file the snippet writes is the file the reader accepts.
	a := Account{Label: "g", ConfigDir: cfg, Family: "glm"}
	s, err := ReadGLM(a)
	if err != nil || s.Primary == nil || s.Primary.UsedPct != 42 || s.Primary.Minutes != 300 {
		t.Fatalf("reader on snippet output: %+v %v (%s)", s, err, got)
	}
	argv, _ := os.ReadFile(filepath.Join(bin, "calls"))
	if strings.Contains(string(argv), "SECRET-TOKEN") {
		t.Errorf("token reached curl's argv: %s", argv)
	}
	if !strings.Contains(string(argv), "https://api.z.ai/api/monitor/usage/quota/limit") {
		t.Errorf("endpoint is the origin of ANTHROPIC_BASE_URL + the quota path: %s", argv)
	}
	in, _ := os.ReadFile(filepath.Join(bin, "stdin"))
	if !strings.Contains(string(in), "Authorization: SECRET-TOKEN-123") {
		t.Errorf("token not passed on stdin: %q", in)
	}
	if _, err := os.Stat(filepath.Join(cfg, "hadron-usage", "glm-quota.json.tmp")); err == nil {
		t.Error("temp file left behind")
	}
}

func TestGLMSnippetThrottlesByMtime(t *testing.T) {
	cfg, bin := snippetEnv(t)
	fakeCurl(t, bin)
	_ = os.WriteFile(filepath.Join(bin, "reply"), []byte(quotaReply), 0o600)
	env := []string{"ANTHROPIC_AUTH_TOKEN=t", "ANTHROPIC_BASE_URL=https://api.z.ai/api/anthropic"}
	runSnippet(t, cfg, bin, env...)
	runSnippet(t, cfg, bin, env...)
	if n := calls(bin); n != 1 {
		t.Fatalf("second run inside 5 minutes called the endpoint (%d calls)", n)
	}
	old := time.Now().Add(-6 * time.Minute)
	if err := os.Chtimes(filepath.Join(cfg, "hadron-usage", "glm-quota.json"), old, old); err != nil {
		t.Fatal(err)
	}
	runSnippet(t, cfg, bin, env...)
	if n := calls(bin); n != 2 {
		t.Fatalf("a 6-minute-old file must be refreshed (%d calls)", n)
	}
}

func TestGLMSnippetFailsClosed(t *testing.T) {
	cfg, bin := snippetEnv(t)
	fakeCurl(t, bin)
	_ = os.WriteFile(filepath.Join(bin, "reply"), []byte(quotaReply), 0o600)
	env := []string{"ANTHROPIC_AUTH_TOKEN=t", "ANTHROPIC_BASE_URL=https://api.z.ai/api/anthropic"}
	runSnippet(t, cfg, bin, env...)
	file := filepath.Join(cfg, "hadron-usage", "glm-quota.json")
	before, _ := os.ReadFile(file)
	stale := func() {
		old := time.Now().Add(-10 * time.Minute)
		if err := os.Chtimes(file, old, old); err != nil {
			t.Fatal(err)
		}
	}
	for name, reply := range map[string]string{
		"curl failure":        "", // no reply file: curl exits 22
		"error body":          `{"code":401,"success":false,"msg":"token expired"}`,
		"not json":            `<html>502</html>`,
		"limits not an array": `{"data":{"limits":"x"}}`,
	} {
		stale()
		rp := filepath.Join(bin, "reply")
		_ = os.Remove(rp)
		if name != "curl failure" {
			_ = os.WriteFile(rp, []byte(reply), 0o600)
		}
		runSnippet(t, cfg, bin, env...)
		after, err := os.ReadFile(file)
		if err != nil || string(after) != string(before) {
			t.Errorf("%s: the previous file must stay untouched; got %q err %v", name, after, err)
		}
		if _, err := os.Stat(file + ".tmp"); err == nil {
			t.Errorf("%s: temp file left behind", name)
		}
	}
}

func TestGLMSnippetNeedsKeyAndBase(t *testing.T) {
	cfg, bin := snippetEnv(t)
	fakeCurl(t, bin)
	_ = os.WriteFile(filepath.Join(bin, "reply"), []byte(quotaReply), 0o600)
	runSnippet(t, cfg, bin) // a session without the glm env makes no call
	runSnippet(t, cfg, bin, "ANTHROPIC_AUTH_TOKEN=t")
	if n := calls(bin); n != 0 {
		t.Fatalf("no key/base must mean no call (%d)", n)
	}
	if _, err := os.Stat(filepath.Join(cfg, "hadron-usage", "glm-quota.json")); err == nil {
		t.Fatal("no file may be written without a successful call")
	}
}
