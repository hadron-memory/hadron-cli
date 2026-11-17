package hostpoller

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"
	"unicode"
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
	for _, want := range []string{"output collection incomplete", "stop not confirmed, records kept", "partial-output"} {
		if !strings.Contains(detail, want) {
			t.Errorf("missing %q: %q", want, detail)
		}
	}
	if stopped {
		t.Fatal("incomplete output confirmed a stop")
	}
	for _, r := range detail {
		if unicode.IsControl(r) {
			t.Errorf("unsafe output %q", detail)
		}
	}
}
