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
	want := "@ada Gil had not been told to halt as of 2026-11-04T12:05:00Z and may still be running: its HALT notice (event e) was not typed into its session as of then (draft: unsent text is in its input box, since 1970-01-01T00:00:00Z). It may have been typed since. The host retries every poll and this is the only post about it, so check Gil's terminal."
	if body != want {
		t.Fatalf("body = %q", body)
	}
}

// #1660 ask 5, LOCAL P3-1 (Jonas): the ruling pins the as-of stamp to RFC3339
// UTC, but the fixtures above pass UTC times, so dropping the .UTC() conversion
// would pass them while a local-clock reporter printed its offset. Pin the
// conversion with a non-UTC now (13:05 CET must post as 12:05Z).
func TestStopDropBodyAsOfStampIsUTCWhateverTheClockSays_1660(t *testing.T) {
	now := time.Date(2026, 11, 4, 13, 5, 0, 0, time.FixedZone("CET", 1*60*60))
	body := stopDropBody("ada", StopDrop{EventID: "e", Worker: "Gil", Level: "HALT", HoldReason: "draft", HeldAt: time.Unix(0, 0)}, now)
	want := "@ada Gil had not been told to halt as of 2026-11-04T12:05:00Z and may still be running: its HALT notice (event e) was not typed into its session as of then (draft: unsent text is in its input box, since 1970-01-01T00:00:00Z). It may have been typed since. The host retries every poll and this is the only post about it, so check Gil's terminal."
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
	want := "@ada Gil had not been told to halt as of 2026-11-04T12:00:00Z and may still be running: its HALT notice (event ev-1) was not typed into its session as of then (draft: unsent text is in its input box, since 2026-11-04T12:00:00Z). It may have been typed since. The host retries every poll and this is the only post about it, so check Gil's terminal."
	if len(bodies) != 1 || bodies[0] != want {
		t.Fatalf("bodies = %q", bodies)
	}
}
