package hostpoller

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"
	"unicode"
	"unicode/utf8"
)

func TestTreeKillCancellationContext2128(t *testing.T) {
	for _, tt := range []struct {
		name string
		out  []byte
		err  error
		want []string
	}{
		{"error", nil, errors.New("runner-sentinel"), []string{"runner-sentinel"}},
		{"output", []byte("  output-sentinel  \n"), nil, []string{"output-sentinel"}},
		{"unsafe", []byte(" output-sentinel\n\x1b[31msecond\r\u2028line "), errors.New("runner-sentinel\t\x1b[1merror"), []string{"output-sentinel", "runner-sentinel"}},
		{"empty", nil, nil, nil},
		{"wait-delay", []byte("output-sentinel"), exec.ErrWaitDelay, []string{"output-sentinel", exec.ErrWaitDelay.Error()}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			stopped, detail := runTreeKill(4242, 0, func(ctx context.Context, pid int) ([]byte, error) {
				if ctx.Err() != context.DeadlineExceeded || pid != 4242 {
					t.Fatalf("runner input pid=%d err=%v", pid, ctx.Err())
				}
				return tt.out, tt.err
			})
			if stopped || !strings.Contains(detail, "received cancellation") || !strings.Contains(detail, "stop not confirmed, records kept") {
				t.Fatalf("cancellation precedence lost: stopped=%v detail=%q", stopped, detail)
			}
			for _, want := range tt.want {
				if !strings.Contains(detail, want) {
					t.Errorf("lost runner context %q: %q", want, detail)
				}
			}
			if tt.name == "empty" && strings.Contains(detail, "runner context") {
				t.Errorf("empty context added misleading detail: %q", detail)
			}
			for _, r := range detail {
				if unicode.IsControl(r) || unicode.In(r, unicode.Cf, unicode.Zl, unicode.Zp) {
					t.Errorf("unsafe diagnostic rune %U: %q", r, detail)
				}
			}
		})
	}
}

func TestTreeKillWaitDelayUnconfirmed2128(t *testing.T) {
	stopped, detail := runTreeKill(4242, time.Second, func(ctx context.Context, _ int) ([]byte, error) {
		if ctx.Err() != nil {
			t.Fatal(ctx.Err())
		}
		return []byte("partial-output\n\x1b[31m"), errors.Join(errors.New("runner-sentinel"), exec.ErrWaitDelay)
	})
	for _, want := range []string{"output collection incomplete", "stop not confirmed, records kept", "partial-output", "runner-sentinel"} {
		if !strings.Contains(detail, want) {
			t.Errorf("missing %q: %q", want, detail)
		}
	}
	if stopped {
		t.Fatal("incomplete output confirmed a stop")
	}
	// #2316 B2: the live-context path is the WaitDelay branch, not cancellation.
	if strings.Contains(detail, "received cancellation") {
		t.Errorf("live-context WaitDelay reported as cancellation: %q", detail)
	}
	for _, r := range detail {
		if unicode.IsControl(r) {
			t.Errorf("unsafe output %q", detail)
		}
	}
}

// hadron-cli#2316 Part A: the ordinary failure (live context, not ErrWaitDelay)
// shows runner text, so it is single-line and bounded like the other branches.
func TestTreeKillOrdinaryFailureSanitized2316(t *testing.T) {
	const unsafe = "\r\n\x1b[31m\u200b\u2028\u2029"
	for _, tt := range []struct {
		name string
		out  []byte
		err  error
		want []string
	}{
		{"both", []byte("  out-sentinel" + unsafe + "second  \n"), errors.New("err-sentinel" + unsafe + "tail"), []string{"err-sentinel", "out-sentinel", "second", "tail"}},
		{"error-only", nil, errors.New("err-sentinel" + unsafe + "tail"), []string{"err-sentinel", "tail"}},
		{"output-only", []byte("out-sentinel" + unsafe + "second"), errors.New("x"), []string{"out-sentinel", "second"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			stopped, detail := runTreeKill(4242, time.Second, func(ctx context.Context, _ int) ([]byte, error) {
				if ctx.Err() != nil {
					t.Fatal(ctx.Err())
				}
				return tt.out, tt.err
			})
			if stopped || !strings.Contains(detail, "could not stop the launched process tree (pid 4242)") {
				t.Fatalf("ordinary failure shape lost: stopped=%v detail=%q", stopped, detail)
			}
			for _, want := range tt.want {
				if !strings.Contains(detail, want) {
					t.Errorf("lost runner text %q: %q", want, detail)
				}
			}
			for _, r := range detail {
				if unicode.IsControl(r) || unicode.In(r, unicode.Cf, unicode.Zl, unicode.Zp) {
					t.Errorf("unsafe diagnostic rune %U: %q", r, detail)
				}
			}
			if strings.HasSuffix(detail, ": ") || strings.HasSuffix(detail, ":") {
				t.Errorf("stray separator with nothing after it: %q", detail)
			}
		})
	}
	t.Run("empty-output", func(t *testing.T) {
		_, detail := runTreeKill(4242, time.Second, func(context.Context, int) ([]byte, error) { return []byte(" \n"), errors.New("err-sentinel") })
		if !strings.Contains(detail, "err-sentinel") || strings.HasSuffix(strings.TrimSpace(detail), ":") || strings.Contains(detail, ": ;") {
			t.Errorf("empty output left an artifact: %q", detail)
		}
	})
}

// boundedPart returns the runner text after marker, up to the next "; ".
func boundedPart(t *testing.T, detail, marker string) string {
	t.Helper()
	_, after, ok := strings.Cut(detail, marker)
	if !ok {
		t.Fatalf("missing %q in %.80q", marker, detail)
	}
	if i := strings.Index(after, "; "); i >= 0 {
		after = after[:i]
	}
	return after
}

func want512(t *testing.T, part string) {
	t.Helper()
	if n := utf8.RuneCountInString(part); n != 512 || !strings.HasSuffix(part, "…") {
		t.Errorf("part has %d runes (want 512 ending in an ellipsis)", n)
	}
}

// hadron-cli#2316 Part B1 (coverage only, baseline already correct): error and
// output are each bounded to 512 runes, independently, in the two branches that
// already bound them.
func TestTreeKillRunnerTextBoundedIndependently2316(t *testing.T) {
	long := strings.Repeat("é", 5000)
	for _, branch := range []struct {
		name    string
		timeout time.Duration
		err     func(error) error
	}{
		{"cancellation", 0, func(e error) error { return e }},
		{"wait-delay", time.Second, func(e error) error { return errors.Join(e, exec.ErrWaitDelay) }},
	} {
		t.Run(branch.name+"/error", func(t *testing.T) {
			_, detail := runTreeKill(4242, branch.timeout, func(context.Context, int) ([]byte, error) { return nil, branch.err(errors.New(long)) })
			want512(t, boundedPart(t, detail, "error: "))
		})
		t.Run(branch.name+"/output", func(t *testing.T) {
			var err error
			if branch.name == "wait-delay" {
				err = exec.ErrWaitDelay
			}
			_, detail := runTreeKill(4242, branch.timeout, func(context.Context, int) ([]byte, error) { return []byte(long), err })
			want512(t, boundedPart(t, detail, "output: "))
		})
	}
}

// hadron-cli#2316 Part A: the ordinary branch is bounded the same way. The
// assertion counts kept runes, so it does not depend on how the parts are joined.
func TestTreeKillOrdinaryFailureBounded2316(t *testing.T) {
	long := strings.Repeat("é", 5000)
	for _, tt := range []struct {
		name string
		out  []byte
		err  error
	}{
		{"error", nil, errors.New(long)},
		{"output", []byte(long), errors.New("x")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, detail := runTreeKill(4242, time.Second, func(context.Context, int) ([]byte, error) { return tt.out, tt.err })
			// 512 runes per part: 511 kept plus the ellipsis.
			if n, e := strings.Count(detail, "é"), strings.Count(detail, "…"); n != 511 || e != 1 {
				t.Errorf("kept %d runes and %d ellipses (want 511 and 1)", n, e)
			}
		})
	}
}
