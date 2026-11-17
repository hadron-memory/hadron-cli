package delivery

import (
	"testing"
	"time"
)

// #1660 ask 5: the un-typed stop-drop branch posts exactly once and nothing
// ever corrects it, so it must scope its claim to the moment of the post
// instead of asserting the notice is un-typed for good (a notice typed after
// the post — or typed but held from ack, so TypedAt is still zero — would
// otherwise read as never typed).
func TestStopDropBodyScopesTheUntypedClaimToThePostTime_1660(t *testing.T) {
	now := time.Date(2026, 11, 4, 12, 5, 0, 0, time.UTC)
	body := stopDropBody("ada", StopDrop{EventID: "e", Worker: "Gil", Level: "HALT", HoldReason: "draft", HeldAt: time.Unix(0, 0)}, now)
	want := "@ada Gil has not been told to halt as of 2026-11-04T12:05:00Z and may still be running: its HALT notice (event e) was not typed into its session as of then (draft: unsent text is in its input box, since 1970-01-01T00:00:00Z). The host retries every poll, but this is the only post about it; check Gil's terminal."
	if body != want {
		t.Fatalf("body = %q", body)
	}
}

// The as-of stamp is the reporter's own post moment (Now), never a zero time:
// the flag must say when the claim held, which is when it was posted.
func TestStopReporterScopesUntypedDropToItsPostTime_1660(t *testing.T) {
	f := newReporterFixture(t)
	f.run(f.reporter())
	bodies := f.chat.bodies()
	want := "@ada Gil has not been told to halt as of 2026-11-04T12:00:00Z and may still be running: its HALT notice (event ev-1) was not typed into its session as of then (draft: unsent text is in its input box, since 2026-11-04T12:00:00Z). The host retries every poll, but this is the only post about it; check Gil's terminal."
	if len(bodies) != 1 || bodies[0] != want {
		t.Fatalf("bodies = %q", bodies)
	}
}
