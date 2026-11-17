package delivery

import (
	"strings"
	"testing"
	"time"
)

// #1660 ask 5: the un-typed stop-drop branch posts exactly once and nothing
// ever corrects it, so it must scope its claim to the moment of the post
// instead of asserting the notice is un-typed for good (a notice typed after
// the post — or typed but held from ack, so TypedAt is still zero — would
// otherwise read as never typed). Wording per Uma's rulings: option B
// (#112587), notice clause amended (#112917), full sentence re-amended
// (#113270) after the BLIND delta P2 (Joaquin, 21d7dc97).
func TestStopDropBodyScopesTheUntypedClaimToThePostTime_1660(t *testing.T) {
	now := time.Date(2026, 11, 4, 12, 5, 0, 0, time.UTC)
	body := stopDropBody("ada", StopDrop{EventID: "e", Worker: "Gil", Level: "HALT", HoldReason: "draft", HeldAt: time.Unix(0, 0)}, now)
	want := "@ada Gil had no acknowledged HALT notice as of 2026-11-04T12:05:00Z and may still be running: its notice (event e) was held (draft: unsent text is in its input box, since 1970-01-01T00:00:00Z). It may have been typed into its session, before or after then. The host retries every poll and this is the only post about it, so check Gil's terminal."
	if body != want {
		t.Fatalf("body = %q", body)
	}
}

// #1660 ask 5, BLIND delta P3 (Joaquin, 21d7dc97): the ack regression must
// not duplicate the scoping test above — and it must keep the overclaim out.
// TypedAt is stamped only after an ack, so a notice typed but held from ack
// has TypedAt == 0 and takes this branch; "had not been told to halt" can be
// false in that state (the worker may already hold the halt text), so the
// body claims only what the host knows — no acknowledged notice as of T —
// and widens the typing hedge to "before or after then" (Uma, #113270).
func TestStopDropBodyClaimsOnlyWhatAnUnackedRecordEstablishes_1660(t *testing.T) {
	now := time.Date(2026, 11, 4, 12, 5, 0, 0, time.UTC)
	body := stopDropBody("ada", StopDrop{EventID: "ev-9", Worker: "Hana", Level: "HALT", HoldReason: "draft", HeldAt: time.Date(2026, 11, 4, 11, 0, 0, 0, time.UTC)}, now)
	want := "@ada Hana had no acknowledged HALT notice as of 2026-11-04T12:05:00Z and may still be running: its notice (event ev-9) was held (draft: unsent text is in its input box, since 2026-11-04T11:00:00Z). It may have been typed into its session, before or after then. The host retries every poll and this is the only post about it, so check Hana's terminal."
	if body != want {
		t.Fatalf("body = %q", body)
	}
	for _, overclaim := range []string{
		"had not been told",       // false when typing preceded a held ack (BLIND P2)
		"It may have been typed since.", // superseded hedge: typing may predate T too
	} {
		if strings.Contains(body, overclaim) {
			t.Fatalf("body overclaims: contains %q", overclaim)
		}
	}
}

// #1660 ask 5, LOCAL P3-1 (Jonas): the ruling pins the as-of stamp to RFC3339
// UTC, but the fixtures above pass UTC times, so dropping the .UTC() conversion
// would pass them while a local-clock reporter printed its offset. Pin the
// conversion with a non-UTC now (13:05 CET must post as 12:05Z).
func TestStopDropBodyAsOfStampIsUTCWhateverTheClockSays_1660(t *testing.T) {
	now := time.Date(2026, 11, 4, 13, 5, 0, 0, time.FixedZone("CET", 1*60*60))
	body := stopDropBody("ada", StopDrop{EventID: "e", Worker: "Gil", Level: "HALT", HoldReason: "draft", HeldAt: time.Unix(0, 0)}, now)
	want := "@ada Gil had no acknowledged HALT notice as of 2026-11-04T12:05:00Z and may still be running: its notice (event e) was held (draft: unsent text is in its input box, since 1970-01-01T00:00:00Z). It may have been typed into its session, before or after then. The host retries every poll and this is the only post about it, so check Gil's terminal."
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
	want := "@ada Gil had no acknowledged HALT notice as of 2026-11-04T12:00:00Z and may still be running: its notice (event ev-1) was held (draft: unsent text is in its input box, since 2026-11-04T12:00:00Z). It may have been typed into its session, before or after then. The host retries every poll and this is the only post about it, so check Gil's terminal."
	if len(bodies) != 1 || bodies[0] != want {
		t.Fatalf("bodies = %q", bodies)
	}
}
