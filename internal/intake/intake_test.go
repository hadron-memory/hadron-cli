package intake

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// adapter writes an executable POSIX shell script and returns a Runner on it.
// body is the script after the shebang; $1 is the call's name.
func adapter(t *testing.T, body string) *Runner {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell-script adapter fixtures need a POSIX shell")
	}
	p := filepath.Join(t.TempDir(), "adapter")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return &Runner{Path: p, Timeout: 5 * time.Second}
}

func TestDescribeSkewIsItsOwnError(t *testing.T) {
	r := adapter(t, `cat >/dev/null; echo '{"name":"fake","contractVersion":2}'`)
	_, err := r.Describe(context.Background())
	var skew *SkewError
	if !errors.As(err, &skew) || skew.Got != 2 {
		t.Fatalf("want SkewError{2}, got %v", err)
	}
}

func TestDescribeMissingVersionIsSkew(t *testing.T) {
	r := adapter(t, `cat >/dev/null; echo '{"name":"fake"}'`)
	var skew *SkewError
	if _, err := r.Describe(context.Background()); !errors.As(err, &skew) {
		t.Fatalf("an adapter that names no version must not be assumed to speak ours: %v", err)
	}
}

func TestHostileOutputIsRefused(t *testing.T) {
	cases := map[string]string{
		"oversized":            `cat >/dev/null; head -c 3000000 /dev/zero | tr '\0' 'a'`,
		"endless":              `cat >/dev/null; yes '{"items":[]}'`,
		"not json":             `cat >/dev/null; echo 'hello'`,
		"array not object":     `cat >/dev/null; echo '[]'`,
		"two documents":        `cat >/dev/null; echo '{"items":[]}{"items":[]}'`,
		"trailing junk":        `cat >/dev/null; echo '{"items":[]} junk'`,
		"truncated":            `cat >/dev/null; printf '{"items":[{"externalId":"u"'`,
		"empty":                `cat >/dev/null`,
		"invalid utf8":         `cat >/dev/null; printf '{"items":[{"externalId":"\xff\xfe","title":"t"}]}'`,
		"empty externalId":     `cat >/dev/null; echo '{"items":[{"externalId":"","title":"t"}]}'`,
		"missing externalId":   `cat >/dev/null; echo '{"items":[{"title":"t"}]}'`,
		"huge externalId":      `cat >/dev/null; printf '{"items":[{"externalId":"'; head -c 5000 /dev/zero | tr '\0' 'a'; printf '","title":"t"}]}'`,
		"externalId 513 chars": `cat >/dev/null; printf '{"items":[{"externalId":"'; head -c 513 /dev/zero | tr '\0' 'a'; printf '","title":"t"}]}'`,
		"huge title":           `cat >/dev/null; printf '{"items":[{"externalId":"u","title":"'; head -c 5000 /dev/zero | tr '\0' 'a'; printf '"}]}'`,
		"huge cursor":          `cat >/dev/null; printf '{"items":[],"nextCursor":"'; head -c 9000 /dev/zero | tr '\0' 'a'; printf '"}'`,
		"too many items":       `cat >/dev/null; printf '{"items":['; i=0; while [ $i -lt 101 ]; do [ $i -gt 0 ] && printf ','; printf '{"externalId":"u%d","title":"t"}' $i; i=$((i+1)); done; printf ']}'`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			r := adapter(t, body)
			start := time.Now()
			_, err := r.ListUntracked(context.Background(), "", "", 20)
			if err == nil {
				t.Fatal("hostile output was accepted")
			}
			var oe *OutputError
			if !errors.As(err, &oe) {
				t.Fatalf("want an OutputError, got %T %v", err, err)
			}
			if time.Since(start) > 4*time.Second {
				t.Fatalf("took %s: the cap must stop an endless writer, not wait for the timeout", time.Since(start))
			}
		})
	}
}

func TestAdapterExitIsARefusalWithSafeDetail(t *testing.T) {
	r := adapter(t, `cat >/dev/null; printf 'boom \033[31mred\033[0m' >&2; exit 3`)
	_, err := r.ListUntracked(context.Background(), "", "", 20)
	var re *RefusedError
	if !errors.As(err, &re) {
		t.Fatalf("want RefusedError, got %v", err)
	}
	if strings.ContainsRune(re.Detail, 0x1b) || !strings.Contains(re.Detail, "exit status 3") {
		t.Fatalf("detail must be terminal-safe and carry the exit: %q", re.Detail)
	}
}

func TestNoAnswerWithinTimeoutIsARefusal(t *testing.T) {
	r := adapter(t, `cat >/dev/null; sleep 5`)
	r.Timeout = 200 * time.Millisecond
	_, err := r.ListUntracked(context.Background(), "", "", 20)
	var re *RefusedError
	if !errors.As(err, &re) {
		t.Fatalf("want RefusedError, got %v", err)
	}
}

func TestMissingExecutableIsARefusal(t *testing.T) {
	r := &Runner{Path: filepath.Join(t.TempDir(), "nope")}
	var re *RefusedError
	if _, err := r.Describe(context.Background()); !errors.As(err, &re) {
		t.Fatalf("want RefusedError, got %v", err)
	}
}

func TestNoShellNoArgumentsButTheCall(t *testing.T) {
	// The script records its argv; a request value shaped like shell syntax
	// must never reach it as an argument.
	out := filepath.Join(t.TempDir(), "argv")
	r := adapter(t, `cat >/dev/null; printf '%s|' "$@" > `+out+`; echo '{"items":[]}'`)
	if _, err := r.ListUntracked(context.Background(), "$(touch /tmp/x);`id`", "a b; c", 5); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(out)
	if string(got) != "list-untracked|" {
		t.Fatalf("argv = %q, want only the call name", got)
	}
}

func TestRequestIsOneJSONObjectOnStdin(t *testing.T) {
	in := filepath.Join(t.TempDir(), "stdin")
	r := adapter(t, `cat > `+in+`; echo '{"items":[]}'`)
	if _, err := r.ListUntracked(context.Background(), "c1", "Marco", 7); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(in)
	for _, want := range []string{`"contractVersion":1`, `"cursor":"c1"`, `"for":"Marco"`, `"limit":7`} {
		if !strings.Contains(string(got), want) {
			t.Fatalf("request %s lacks %s", got, want)
		}
	}
	if strings.Contains(string(got), `"offset"`) || strings.Contains(string(got), `"skip"`) {
		t.Fatalf("an offset must never reach the adapter: %s", got)
	}
}

func TestAdapterGetsNoHadronCredential(t *testing.T) {
	t.Setenv("HADRON_TOKEN", "secret-token")
	t.Setenv("HADRON_API_KEY", "secret-key")
	t.Setenv("GITHUB_TOKEN", "the-teams-own")
	out := filepath.Join(t.TempDir(), "env")
	r := adapter(t, `cat >/dev/null; env > `+out+`; echo '{"items":[]}'`)
	if _, err := r.ListUntracked(context.Background(), "", "", 5); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(out)
	if strings.Contains(string(got), "secret-token") || strings.Contains(string(got), "secret-key") {
		t.Fatalf("a Hadron credential reached the adapter:\n%s", got)
	}
	if !strings.Contains(string(got), "GITHUB_TOKEN=the-teams-own") {
		t.Fatal("the team's own tracker credential must still reach the adapter")
	}
}

func TestWalkFollowsOpaqueCursorsToTheEnd(t *testing.T) {
	r := adapter(t, `
req=$(cat)
case "$req" in
  *'"cursor":"B"'*) echo '{"items":[{"externalId":"u3","title":"c"}]}';;
  *'"cursor":"A"'*) echo '{"items":[{"externalId":"u2","title":"b"}],"nextCursor":"B"}';;
  *) echo '{"items":[{"externalId":"u1","title":"a"}],"nextCursor":"A"}';;
esac`)
	var got []string
	err := r.Walk(context.Background(), "", 1, func(p Page) (bool, error) {
		for _, it := range p.Items {
			got = append(got, it.ExternalID)
		}
		return false, nil
	})
	if err != nil || strings.Join(got, ",") != "u1,u2,u3" {
		t.Fatalf("got %v err %v", got, err)
	}
}

func TestWalkRefusesACursorLoop(t *testing.T) {
	r := adapter(t, `cat >/dev/null; echo '{"items":[{"externalId":"u1","title":"a"}],"nextCursor":"same"}'`)
	pages := 0
	err := r.Walk(context.Background(), "", 1, func(Page) (bool, error) { pages++; return false, nil })
	var oe *OutputError
	if !errors.As(err, &oe) || pages > 2 {
		t.Fatalf("a repeated cursor must be refused after at most two pages, got pages=%d err=%v", pages, err)
	}
}

func TestWalkRefusesAnEndlessFreshCursorList(t *testing.T) {
	// Every page names a new cursor and one new item: no repeat to catch, only
	// the page bound.
	r := adapter(t, `
req=$(cat); n=$(printf '%s' "$req" | tr -cd '0-9' | wc -c)
printf '{"items":[{"externalId":"u%s","title":"t"}],"nextCursor":"c%s"}' "$$" "$$$n$RANDOM"`)
	pages := 0
	err := r.Walk(context.Background(), "", 1, func(Page) (bool, error) { pages++; return false, nil })
	var oe *OutputError
	if !errors.As(err, &oe) || pages != MaxPages {
		t.Fatalf("want a refusal at %d pages, got pages=%d err=%v", MaxPages, pages, err)
	}
}

func TestWalkDoesNotOfferARepeatedItemTwice(t *testing.T) {
	r := adapter(t, `
req=$(cat)
case "$req" in
  *'"cursor":"A"'*) echo '{"items":[{"externalId":"u1","title":"a"},{"externalId":"u2","title":"b"}]}';;
  *) echo '{"items":[{"externalId":"u1","title":"a"}],"nextCursor":"A"}';;
esac`)
	var got []string
	_ = r.Walk(context.Background(), "", 1, func(p Page) (bool, error) {
		for _, it := range p.Items {
			got = append(got, it.ExternalID)
		}
		return false, nil
	})
	if strings.Join(got, ",") != "u1,u2" {
		t.Fatalf("got %v", got)
	}
}

func TestWalkStopsWhenAskedAndAsksNoMore(t *testing.T) {
	calls := filepath.Join(t.TempDir(), "calls")
	r := adapter(t, `cat >/dev/null; echo x >> `+calls+`; echo '{"items":[{"externalId":"u1","title":"a"}],"nextCursor":"A"}'`)
	_ = r.Walk(context.Background(), "", 1, func(Page) (bool, error) { return true, nil })
	b, _ := os.ReadFile(calls)
	if n := strings.Count(string(b), "x"); n != 1 {
		t.Fatalf("a stop must end the walk: %d calls", n)
	}
}

func TestMarkTrackedSendsTheServersPortalURLOrNone(t *testing.T) {
	in := filepath.Join(t.TempDir(), "stdin")
	r := adapter(t, `cat > `+in+`; echo '{}'`)
	err := r.MarkTracked(context.Background(), "https://x.test/i/1",
		Task{ID: "t1", Handle: "AT0.1"}, Worker{ID: "w1", Name: "Joran", Role: "cli-engineer", Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(in)
	if strings.Contains(string(got), "portalUrl") {
		t.Fatalf("a null portal URL must be omitted, never built: %s", got)
	}
	for _, want := range []string{`"externalId":"https://x.test/i/1"`, `"id":"t1"`, `"name":"Joran"`, `"role":"cli-engineer"`, `"model":"m"`} {
		if !strings.Contains(string(got), want) {
			t.Fatalf("request %s lacks %s", got, want)
		}
	}
}

func TestEmptyConfiguredPathIsRefused(t *testing.T) {
	var re *RefusedError
	if _, err := (&Runner{}).Describe(context.Background()); !errors.As(err, &re) {
		t.Fatalf("got %v", err)
	}
}

func TestExternalIDAtServerCapIsAccepted(t *testing.T) {
	r := adapter(t, `cat >/dev/null; printf '{"items":[{"externalId":"'; head -c 512 /dev/zero | tr '\0' 'a'; printf '","title":"t"}]}'`)
	if _, err := r.ListUntracked(context.Background(), "", "", 20); err != nil {
		t.Fatalf("512 characters is within the server cap: %v", err)
	}
}

func TestCallerCancelIsNotAnAdapterRefusal(t *testing.T) {
	r := adapter(t, `cat >/dev/null; sleep 5`)
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(200 * time.Millisecond); cancel() }()
	_, err := r.ListUntracked(ctx, "", "", 20)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %T %v", err, err)
	}
	var re *RefusedError
	if errors.As(err, &re) {
		t.Fatalf("an interrupt must not read as an adapter refusal: %v", err)
	}
}

// The server counts code points, not UTF-16 units: 512 astral characters are
// within its cap (1024 units, 2048 bytes) and must not be refused here.
func TestExternalIDCountsCodePointsLikeTheServer(t *testing.T) {
	r := adapter(t, `cat >/dev/null; printf '{"items":[{"externalId":"'; yes '😀' | head -n 512 | tr -d '\n'; printf '","title":"t"}]}'`)
	if _, err := r.ListUntracked(context.Background(), "", "", 20); err != nil {
		t.Fatalf("512 code points is within the server cap: %v", err)
	}
}
