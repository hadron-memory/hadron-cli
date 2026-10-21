package guardedpush

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

type fake struct {
	now                 time.Time
	frame               Frame
	observe             func(*fake)
	reads               int
	clears, sends       []string
	failClear, failSend bool
	confirm             bool
	lock                bool
	cancel              context.CancelFunc
	cancelAt            string
}

func harness() (*fake, *Engine) {
	f := &fake{now: time.Unix(1, 0), frame: Frame{Identity: "terminal/session", Family: "claude", State: Idle, Empty: true}, confirm: true}
	e := &Engine{Now: func() time.Time { return f.now }, Sleep: func(ctx context.Context, d time.Duration) error { f.now = f.now.Add(d); return ctx.Err() }, Observe: func(context.Context) (Frame, error) {
		f.reads++
		if f.observe != nil {
			f.observe(f)
		}
		return f.frame, nil
	}, Clear: func(_ context.Context, s string) error {
		f.clears = append(f.clears, s)
		f.frame.Draft = ""
		f.frame.Empty = true
		if f.cancelAt == "clear" {
			f.cancel()
		}
		if f.failClear {
			return errors.New("PRIVATE-DRAFT")
		}
		return nil
	}, Submit: func(_ context.Context, s string) error {
		f.sends = append(f.sends, s)
		if f.confirm {
			f.frame.State = Busy
			f.frame.Screen = s + "\nWorking (1s)"
		}
		if f.cancelAt == "send" {
			f.cancel()
		}
		if f.failSend {
			return errors.New("PRIVATE-MESSAGE")
		}
		return nil
	}, Locked: func(_ string, work func(func() bool) Result) Result {
		if f.lock {
			return Result{Outcome: HeldBusy, Phase: "preflight", Reason: "locked"}
		}
		return work(func() bool { return true })
	}}
	return f, e
}
func request() Request {
	return Request{Sender: "Jane", Message: "MESSAGE-CANARY", Attempt: "0123456789abcdef", Seq: 123}
}
func run(e *Engine) Result { return e.Run(context.Background(), request(), Defaults()) }
func TestEmptyOneSendAndFreshConfirmation(t *testing.T) {
	f, e := harness()
	r := run(e)
	if r.Outcome != Sent || len(f.sends) != 1 || len(f.clears) != 0 {
		t.Fatalf("%+v clears%d sends%d", r, len(f.clears), len(f.sends))
	}
}
func TestDraftExactTwoPartsAndMinimumNotSum(t *testing.T) {
	for _, family := range []string{"claude", "codex"} {
		t.Run(family, func(t *testing.T) {
			f, e := harness()
			draft := "unfinished 🐋\nsecond line\n  "
			f.frame.Family = family
			f.frame.Draft = draft
			f.frame.Empty = false
			start := f.now
			r := run(e)
			if r.Outcome != SentWithDraft || len(f.sends) != 1 || len(f.clears) != 1 || f.clears[0] != draft {
				t.Fatalf("%+v", r)
			}
			if f.now.Sub(start) != 30*time.Second {
				t.Fatalf("eligibility %s", f.now.Sub(start))
			}
			s := f.sends[0]
			if strings.Count(s, draft) != 1 || !strings.Contains(s, "not submitted by its author") || !strings.Contains(s, "MESSAGE-CANARY") {
				t.Fatal("two-part payload missing")
			}
		})
	}
}
func TestChangingDraftIsNeverTaken(t *testing.T) {
	f, e := harness()
	f.frame.Empty = false
	f.observe = func(f *fake) { f.frame.Draft = f.now.String() }
	r := run(e)
	if r.Outcome != HeldBusy || len(f.clears)+len(f.sends) != 0 {
		t.Fatalf("%+v", r)
	}
}
func TestDraftChangeResetsStability(t *testing.T) {
	f, e := harness()
	f.frame.Draft = "first"
	f.frame.Empty = false
	start := f.now
	f.observe = func(f *fake) {
		if f.now.Sub(start) >= 29*time.Second && len(f.clears) == 0 {
			f.frame.Draft = "second"
		}
	}
	r := run(e)
	if r.Outcome != SentWithDraft || f.now.Sub(start) < 59*time.Second || f.clears[0] != "second" {
		t.Fatalf("%+v after%s", r, f.now.Sub(start))
	}
}
func TestLateDraftChangeAndIdentityChangeHold(t *testing.T) {
	for _, change := range []string{"draft", "identity", "unknown"} {
		f, e := harness()
		f.observe = func(f *fake) {
			if f.reads == 3 {
				switch change {
				case "draft":
					f.frame.Draft = "human new text"
					f.frame.Empty = false
				case "identity":
					f.frame.Identity = "replacement"
				case "unknown":
					f.frame.State = Unknown
				}
			}
		}
		r := run(e)
		if r.Outcome != HeldUnknown || len(f.sends)+len(f.clears) != 0 {
			t.Fatalf("%s %+v", change, r)
		}
	}
}
func TestBusyAndUnknownHoldWithoutInput(t *testing.T) {
	for _, state := range []State{Busy, Unknown} {
		f, e := harness()
		f.frame.State = state
		r := run(e)
		want := HeldUnknown
		if state == Busy {
			want = HeldBusy
		}
		if r.Outcome != want || len(f.sends)+len(f.clears) != 0 {
			t.Fatalf("%+v", r)
		}
	}
}
func TestBusyThenIdle(t *testing.T) {
	f, e := harness()
	f.frame.State = Busy
	start := f.now
	f.observe = func(f *fake) {
		if f.now.Sub(start) >= time.Second && len(f.sends) == 0 {
			f.frame.State = Idle
		}
	}
	if r := run(e); r.Outcome != Sent || f.now.Sub(start) < time.Second {
		t.Fatalf("%+v", r)
	}
}
func TestClearFailureAndConcurrentNewInputAreUnconfirmed(t *testing.T) {
	for _, mode := range []string{"fail", "new-input", "identity"} {
		f, e := harness()
		f.frame.Draft = "draft"
		f.frame.Empty = false
		f.failClear = mode == "fail"
		f.observe = func(f *fake) {
			if len(f.clears) > 0 {
				if mode == "new-input" {
					f.frame.Draft = "new human input"
					f.frame.Empty = false
				}
				if mode == "identity" {
					f.frame.Identity = "replacement"
				}
			}
		}
		r := run(e)
		if r.Outcome != Unconfirmed || len(f.sends) != 0 {
			t.Fatalf("%s %+v", mode, r)
		}
	}
}
func TestAcceptedReceiptAndOldBusyNeverConfirm(t *testing.T) {
	for _, mode := range []string{"idle", "busy-old"} {
		f, e := harness()
		f.confirm = false
		f.observe = func(f *fake) {
			if len(f.sends) > 0 && mode == "busy-old" {
				f.frame.State = Busy
				f.frame.Screen = "old turn Working (2s)"
			}
		}
		r := run(e)
		if r.Outcome != Unconfirmed || len(f.sends) != 1 {
			t.Fatalf("%+v", r)
		}
	}
}
func TestSendFailureNeverRetries(t *testing.T) {
	f, e := harness()
	f.failSend = true
	r := run(e)
	if r.Outcome != Unconfirmed || len(f.sends) != 1 {
		t.Fatalf("%+v", r)
	}
}
func TestLockContentionHolds(t *testing.T) {
	f, e := harness()
	f.lock = true
	r := run(e)
	if r.Outcome != HeldBusy || len(f.clears)+len(f.sends) != 0 {
		t.Fatalf("%+v", r)
	}
}
func TestUnsupportedDraftClearHolds(t *testing.T) {
	f, e := harness()
	f.frame.Family = "custom"
	f.frame.Draft = "draft"
	f.frame.Empty = false
	r := run(e)
	if r.Outcome != HeldUnknown || len(f.clears)+len(f.sends) != 0 {
		t.Fatalf("%+v", r)
	}
}
func TestWhitespaceDraftIsNotEmpty(t *testing.T) {
	f, e := harness()
	f.frame.Draft = " \n\t"
	f.frame.Empty = false
	if r := run(e); r.Outcome != SentWithDraft || f.clears[0] != " \n\t" {
		t.Fatalf("%+v", r)
	}
}
func TestCancellationBeforeAndAfterMutation(t *testing.T) {
	for _, at := range []string{"before", "clear", "send"} {
		f, e := harness()
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		f.cancel = cancel
		f.cancelAt = at
		if at == "before" {
			cancel()
		}
		if at == "clear" {
			f.frame.Draft = "draft"
			f.frame.Empty = false
		}
		r := e.Run(ctx, request(), Defaults())
		want := Unconfirmed
		if at == "before" {
			want = HeldBusy
		}
		if r.Outcome != want {
			t.Fatalf("%s %+v", at, r)
		}
	}
}
func TestInvalidTimingAndSizeNeverMutate(t *testing.T) {
	for _, bad := range []string{"stability", "wait", "poll", "confirm", "too-long"} {
		f, e := harness()
		o := Defaults()
		q := request()
		switch bad {
		case "stability":
			o.Stable = time.Second
		case "wait":
			o.Wait = 0
		case "poll":
			o.Poll = -1
		case "confirm":
			o.Confirm = 0
		case "too-long":
			q.Message = strings.Repeat("x", 140000)
		}
		r := e.Run(context.Background(), q, o)
		if r.Outcome != HeldUnknown || len(f.clears)+len(f.sends) != 0 {
			t.Fatalf("%s %+v", bad, r)
		}
	}
}

func TestLockLossBeforeMutationAndAfterClear(t *testing.T) {
	for _, after := range []bool{false, true} {
		f, e := harness()
		if after {
			f.frame.Draft = "draft"
			f.frame.Empty = false
		}
		e.Locked = func(_ string, work func(func() bool) Result) Result {
			return work(func() bool {
				if after {
					return len(f.clears) == 0
				}
				return f.reads < 3
			})
		}
		r := run(e)
		want := HeldUnknown
		if after {
			want = Unconfirmed
		}
		if r.Outcome != want || len(f.sends) != 0 {
			t.Fatalf("after%t %+v", after, r)
		}
	}
}
func TestCancellationDuringConfirmationIsUnconfirmed(t *testing.T) {
	f, e := harness()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f.confirm = false
	f.observe = func(f *fake) {
		if len(f.sends) > 0 {
			cancel()
		}
	}
	r := e.Run(ctx, request(), Defaults())
	if r.Outcome != Unconfirmed || len(f.sends) != 1 {
		t.Fatalf("%+v", r)
	}
}
func TestTerminalControlBytesAreNotTyped(t *testing.T) {
	for _, draft := range []bool{false, true} {
		f, e := harness()
		q := request()
		if draft {
			f.frame.Draft = "\x1b[2K"
			f.frame.Empty = false
		} else {
			q.Message = "\x03"
		}
		r := e.Run(context.Background(), q, Defaults())
		if r.Outcome != HeldUnknown || len(f.clears)+len(f.sends) != 0 {
			t.Fatalf("%+v", r)
		}
	}
}
func TestCombinedPayloadTooLargeHoldsBeforeClear(t *testing.T) {
	f, e := harness()
	f.frame.Draft = strings.Repeat("d", 130000)
	f.frame.Empty = false
	q := request()
	q.Message = strings.Repeat("m", 2000)
	r := e.Run(context.Background(), q, Defaults())
	if r.Outcome != HeldUnknown || len(f.clears)+len(f.sends) != 0 {
		t.Fatalf("%+v", r)
	}
}

func TestConfirmationNeedsBusyEvenWithAttemptMarkerAndEmptyDraft(t *testing.T) {
	f, e := harness()
	f.confirm = false
	f.observe = func(f *fake) {
		if len(f.sends) > 0 {
			f.frame.State = Idle
			f.frame.Screen = f.sends[0]
			f.frame.Draft = ""
		}
	}
	r := run(e)
	if r.Outcome != Unconfirmed || r.Reason != "turn-not-confirmed" || len(f.sends) != 1 {
		t.Fatalf("%+v sends%d", r, len(f.sends))
	}
}
func TestDraftWaitIndependentlyBoundsEligibility(t *testing.T) {
	for _, c := range []struct{ wait, stable time.Duration }{{60 * time.Second, 30 * time.Second}, {30 * time.Second, 60 * time.Second}} {
		f, e := harness()
		f.frame.Draft = "draft"
		f.frame.Empty = false
		start := f.now
		o := Defaults()
		o.DraftWait = c.wait
		o.Stable = c.stable
		r := e.Run(context.Background(), request(), o)
		if r.Outcome != SentWithDraft || f.now.Sub(start) < max(c.wait, c.stable) {
			t.Fatalf("%+v duration%s", r, f.now.Sub(start))
		}
	}
}
func TestConfirmationIdentityAndOwnershipChangesCannotConfirm(t *testing.T) {
	for _, change := range []string{"identity", "family", "lock"} {
		f, e := harness()
		f.observe = func(f *fake) {
			if len(f.sends) > 0 {
				if change == "identity" {
					f.frame.Identity = "other"
				}
				if change == "family" {
					f.frame.Family = "codex"
				}
			}
		}
		if change == "lock" {
			e.Locked = func(_ string, work func(func() bool) Result) Result {
				return work(func() bool { return len(f.sends) == 0 })
			}
		}
		r := run(e)
		if r.Outcome != Unconfirmed || len(f.sends) != 1 {
			t.Fatalf("%s %+v", change, r)
		}
	}
}
func TestDraftPrivacyOnRefusalPaths(t *testing.T) {
	for _, kind := range []string{"unsupported", "pre-send-change"} {
		f, e := harness()
		secret := "PRIVATE-DRAFT-CANARY"
		f.frame.Draft = secret
		f.frame.Empty = false
		if kind == "unsupported" {
			f.frame.Draft += "\x1b"
		} else {
			atReady := 0
			f.observe = func(f *fake) {
				if f.now.Sub(time.Unix(1, 0)) >= 30*time.Second {
					atReady++
					if atReady >= 2 {
						f.frame.Draft = secret + "new"
					}
				}
			}
		}
		r := run(e)
		want := "draft-unsupported"
		if kind == "pre-send-change" {
			want = "pre-send-changed"
		}
		if r.Outcome != HeldUnknown || r.Reason != want || len(f.clears)+len(f.sends) != 0 {
			t.Fatalf("%s %+v", kind, r)
		}
		raw, _ := json.Marshal(r)
		if strings.Contains(string(raw), secret) {
			t.Fatal("draft escaped result")
		}
	}
}
