package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func locPageCursor1787(cursor string) string {
	b, _ := jsonMarshalString1787(cursor)
	return `{"data":{"memoryLocSegments":{"items":[{"segment":"a","hasNode":true,"memoryId":"m"}],"hasMore":true,"endCursor":` + b + `}}}`
}

// The footer's cursor must paste back as ONE argument and run nothing (cli#1787).
// Each cursor goes through an actual sh with inert synthetic data.
func TestMemoryLocSegmentsFooterShellSafe1787(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh")
	}
	cursors := []string{
		"opaque cursor with spaces",
		"it's a quote",
		"a;touch PWNED",
		"$(touch PWNED)",
		"`touch PWNED`",
		"x'; touch PWNED; echo '",
		`back\slash "dq" *glob* ?`,
		"b64+/==",
	}
	for _, cursor := range cursors {
		r := runLoc1744(t, locPageCursor1787(cursor), "--memory", "m")
		if r.err != nil {
			t.Fatalf("%q: %v", cursor, r.err)
		}
		line := strings.TrimSuffix(r.stderr, "\n")
		const prefix = "more segments remain — continue with --after "
		if !strings.HasPrefix(line, prefix) {
			t.Fatalf("%q: footer %q", cursor, line)
		}
		quoted := strings.TrimPrefix(line, prefix)
		dir := t.TempDir()
		script := fmt.Sprintf("set -- %s; [ $# -eq 1 ] && printf %%s \"$1\" > got", quoted)
		cmd := exec.Command(sh, "-c", script)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%q: sh failed: %v %s (footer %q)", cursor, err, out, line)
		}
		got, err := os.ReadFile(filepath.Join(dir, "got"))
		if err != nil || string(got) != cursor {
			t.Fatalf("%q: --after received %q (err %v), want the cursor byte-for-byte", cursor, got, err)
		}
		if _, err := os.Stat(filepath.Join(dir, "PWNED")); err == nil {
			t.Fatalf("%q: footer executed injected command", cursor)
		}
	}
}

// A cursor in the safe set prints exactly as before; JSON carries it raw.
func TestMemoryLocSegmentsFooterSafeSetUnchanged1787(t *testing.T) {
	r := runLoc1744(t, locPageCursor1787("abc:DEF._/@-123"), "--memory", "m")
	if r.err != nil || r.stderr != "more segments remain — continue with --after abc:DEF._/@-123\n" {
		t.Fatalf("err %v stderr %q", r.err, r.stderr)
	}
	r = runLoc1744(t, locPageCursor1787("it's a b"), "--memory", "m", "--json")
	if r.err != nil || r.stderr != "" || !strings.Contains(r.out, `"endCursor": "it's a b"`) {
		t.Fatalf("json must carry the raw cursor: err %v stderr %q out %s", r.err, r.stderr, r.out)
	}
}

func jsonMarshalString1787(s string) (string, error) {
	b, err := json.Marshal(s)
	return string(b), err
}
