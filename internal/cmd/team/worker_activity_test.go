package team

import (
	"strings"
	"testing"
	"time"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
)

func ptrBool(b bool) *bool     { return &b }
func ptrStr(s string) *string  { return &s }
func noLabel(id string) string { return id }
func fixedNow() time.Time      { return time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC) }
func atOffset(d time.Duration) *string {
	return ptrStr(fixedNow().Add(-d).Format(time.RFC3339))
}

// The masked row is the case every other branch here is measured against, so
// it gets its own test rather than one row in a table.
//
// A caller who may not read the App's working state gets nulls that look
// exactly like a genuinely unheld, never-driven worker — and answering "nobody"
// or "never" to them is the one failure these cells must never have. It reads
// as a settled fact, it is wrong, and it is wrong in the direction that gets a
// name taken out from under somebody.
func TestMaskedWorkingStateNeverAnswersTheQuestion(t *testing.T) {
	// heldByUserId and lastActiveAt are nil for the SAME reason hasLiveSession
	// is — the gate — so a fixture that masked only some of them would test a
	// shape the server cannot produce.
	held := renderHeldBy(nil, nil, "u-holger", noLabel)
	driven := renderLastDriven(nil, nil, fixedNow())
	if held != unknownCell {
		t.Errorf("a masked hold must render %q, got %q", unknownCell, held)
	}
	if driven != unknownCell {
		t.Errorf("masked liveness must render %q, got %q", unknownCell, driven)
	}
	// Stated positively as well, because the dash check above passes for any
	// wrong answer that happens to contain one.
	for _, banned := range []string{"nobody", "never", "live"} {
		if held == banned || driven == banned {
			t.Errorf("a masked read answered %q — it must say only that it cannot say", banned)
		}
	}
	// "Cannot say" and "definitely no" must not share a glyph. The RETIRED
	// column beside these renders dash() for a DEFINITE answer, so a masked row
	// spelled the same way reads as three settled facts — two of which the
	// server refused to state. This is the assertion that stops the cell
	// quietly reverting to the em-dash it started out as.
	if unknownCell == dash(nil) {
		t.Errorf("the unknown cell (%q) must not be the glyph this table uses for a definite no (%q)",
			unknownCell, dash(nil))
	}

	// The discriminator itself: a non-nil hasLiveSession is what turns those
	// same nils into real answers. If this stops being true, everything above
	// is answering the wrong question.
	if got := renderHeldBy(nil, ptrBool(false), "u-holger", noLabel); got != "nobody" {
		t.Errorf("a PERMITTED read of an unheld name must say nobody, got %q", got)
	}
	if got := renderLastDriven(ptrBool(false), nil, fixedNow()); got != "never" {
		t.Errorf("a PERMITTED read of an undriven name must say never, got %q", got)
	}
}

func TestRenderLastDriven(t *testing.T) {
	for _, tc := range []struct {
		name string
		live *bool
		at   *string
		want string
	}{
		{"masked", nil, nil, unknownCell},
		{"masked even with a timestamp", nil, atOffset(3 * time.Hour), unknownCell},
		{"live", ptrBool(true), atOffset(4 * time.Hour), "live"},
		{"live and never driven", ptrBool(true), nil, "live"},
		{"never", ptrBool(false), nil, "never"},
		{"never, empty string", ptrBool(false), ptrStr(""), "never"},
		{"aged", ptrBool(false), atOffset(72 * time.Hour), "3d ago"},
		{"unparseable falls back to the raw value", ptrBool(false), ptrStr("last tuesday"), "last tuesday"},
	} {
		if got := renderLastDriven(tc.live, tc.at, fixedNow()); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

// A live session prints NO age, and this is the assertion that pins it.
//
// It is separated from the table above because the table would pass if "live"
// became "live (4h ago)" only for a substring check — and an age beside "live"
// is the specific misread the column is designed to prevent: it invites the
// reader to treat an open session as a person at the keyboard.
func TestALiveSessionPrintsNoAge(t *testing.T) {
	got := renderLastDriven(ptrBool(true), atOffset(4*time.Hour), fixedNow())
	if got != "live" {
		t.Fatalf("a live session must render exactly %q, got %q", "live", got)
	}
	// Proven against a timestamp whose age is renderable: if the age were
	// appended, this fixture is one that would show it.
	if age := workerActivityAge(fixedNow().Add(-4*time.Hour), fixedNow()); age != "4h ago" {
		t.Fatalf("fixture check: the timestamp above must have a renderable age, got %q", age)
	}
}

// FRACTIONAL SECONDS parse, and this pins it.
//
// PR #523 review, @copilot: `time.RFC3339`'s layout has no fractional part, so
// a timestamp like `...:05.123Z` was reported as falling through to the raw
// string and losing the age — the column's primary output.
//
// The claim is WRONG, and measured rather than argued: Go's parser accepts a
// fractional second immediately after the seconds field even when the layout
// does not signify one ("When parsing (only), the input may contain a
// fractional second field...", `time` package docs). Verified on go1.26.4
// across `Z`, offset, and 1-to-6-digit fractions; `RFC3339Nano` returns the
// same instants, so the suggested swap would change nothing.
//
// The finding was right about the STAKE, though, and nothing pinned it: the
// fallback path exists, and if this ever stopped parsing, every age would
// quietly become a raw timestamp with no test to notice. So the fix is a test
// rather than the suggested edit.
func TestFractionalSecondsStillRenderAnAge(t *testing.T) {
	base := fixedNow().Add(-72 * time.Hour).UTC()
	for _, layout := range []string{
		"2006-01-02T15:04:05Z07:00",        // no fraction
		"2006-01-02T15:04:05.000Z07:00",    // milliseconds
		"2006-01-02T15:04:05.000000Z07:00", // microseconds
	} {
		at := base.Format(layout)
		if got := renderLastDriven(ptrBool(false), &at, fixedNow()); got != "3d ago" {
			t.Errorf("%s -> %q, want %q (a fractional second must not fall through to the raw value)",
				at, got, "3d ago")
		}
	}
	// A non-UTC offset too, since the DateTime scalar is documented as
	// Date.parse-able rather than canonical ISO UTC — a value passed through
	// from an external provider keeps that provider's offset.
	offset := base.In(time.FixedZone("CET", 2*3600)).Format("2006-01-02T15:04:05.000-07:00")
	if got := renderLastDriven(ptrBool(false), &offset, fixedNow()); got != "3d ago" {
		t.Errorf("%s -> %q, want %q (instants are compared, not strings)", offset, got, "3d ago")
	}
}

func TestRenderHeldBy(t *testing.T) {
	for _, tc := range []struct {
		name   string
		held   *string
		live   *bool
		selfID string
		want   string
	}{
		{"masked", nil, nil, "u-holger", unknownCell},
		{"masked while held", ptrStr("u-dara"), nil, "u-holger", unknownCell},
		{"unheld", nil, ptrBool(false), "u-holger", "nobody"},
		{"unheld, empty string", ptrStr(""), ptrBool(false), "u-holger", "nobody"},
		{"mine", ptrStr("u-holger"), ptrBool(false), "u-holger", "you"},
		{"someone else's", ptrStr("u-dara"), ptrBool(false), "u-holger", "u-dara"},
		// currentUserID's two non-id states — a failed self-read and an App key
		// — both arrive here as "". Neither is evidence about whose hold this
		// is, so the cell must not claim it is mine and must not claim it is
		// not: it names the holder and stops. The two states are ONE case at
		// this call site, which is exactly why the `known` flag is not a
		// parameter (see renderHeldBy).
		{"caller has no id, and the hold is in fact theirs", ptrStr("u-holger"), ptrBool(false), "", "u-holger"},
		{"caller has no id, hold is another's", ptrStr("u-dara"), ptrBool(false), "", "u-dara"},
	} {
		got := renderHeldBy(tc.held, tc.live, tc.selfID, noLabel)
		if got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

// The label is decoration, and it must be resolved once per DISTINCT holder —
// not per row, and never for the caller's own hold.
func TestHolderLabellerResolvesOncePerDistinctHolder(t *testing.T) {
	calls := map[string]int{}
	label := holderLabeller(func(id string) string {
		calls[id]++
		return "Name(" + id + ")"
	})
	rows := []*string{
		ptrStr("u-dara"), ptrStr("u-dara"), ptrStr("u-gil"),
		ptrStr("u-holger"), // mine — must not reach the resolver at all
		ptrStr("u-dara"),
	}
	var out []string
	for _, r := range rows {
		out = append(out, renderHeldBy(r, ptrBool(false), "u-holger", label))
	}
	if calls["u-dara"] != 1 {
		t.Errorf("a repeated holder must be resolved once, got %d calls", calls["u-dara"])
	}
	if calls["u-gil"] != 1 {
		t.Errorf("a distinct holder must be resolved, got %d calls", calls["u-gil"])
	}
	if calls["u-holger"] != 0 {
		t.Errorf("my own hold renders 'you' and must cost no lookup, got %d calls", calls["u-holger"])
	}
	if out[0] != "Name(u-dara)" || out[3] != "you" {
		t.Errorf("labels: %v", out)
	}
	// A resolver that FAILS returns the raw id, and that answer is memoized
	// too — otherwise a broken lookup is re-paid on every row.
	failCalls := 0
	failing := holderLabeller(func(id string) string { failCalls++; return id })
	for i := 0; i < 3; i++ {
		if got := renderHeldBy(ptrStr("u-dara"), ptrBool(false), "u-holger", failing); got != "u-dara" {
			t.Errorf("a failed lookup must fall back to the raw id, got %q", got)
		}
	}
	if failCalls != 1 {
		t.Errorf("a failed lookup must not be re-paid per row, got %d calls", failCalls)
	}
}

func TestWorkerActivityAge(t *testing.T) {
	now := fixedNow()
	for _, tc := range []struct {
		d    time.Duration
		want string
	}{
		{0, "just now"},
		{59 * time.Second, "just now"},
		{time.Minute, "1m ago"},
		{59 * time.Minute, "59m ago"},
		{90 * time.Minute, "1h ago"}, // rounds DOWN — never overstates recency
		{time.Hour, "1h ago"},
		{23 * time.Hour, "23h ago"},
		{24 * time.Hour, "1d ago"},
		{47 * time.Hour, "1d ago"},
		{30 * 24 * time.Hour, "30d ago"},
		// Clock skew between the server and this host. A negative age must not
		// render as "-1m ago"; "just now" is the honest floor.
		{-5 * time.Minute, "just now"},
	} {
		if got := workerActivityAge(now.Add(-tc.d), now); got != tc.want {
			t.Errorf("age(%v): got %q, want %q", tc.d, got, tc.want)
		}
	}
}

// drivenBy has FOUR outcomes because a nil `active` has three different causes
// (#553), and three of them used to render as one sentence.
//
// The table is the ruling: a failed read, a masked driver and a genuinely
// absent one are different facts, and only the last is "an unknown driver".
// Reporting either of the first two that way makes a claim about the WORKER out
// of a fact about the read — review:a-claim-must-not-outrun-its-evidence.
func TestDrivenByDistinguishesFailedMaskedAndAbsent(t *testing.T) {
	row := gen.TeamSessionFields{Id: "s-old", UserId: ptrStr("u-holger"), StartedAt: "2026-08-11T09:00:00Z"}
	answered := func(active *gen.TeamSessionFields) sessionNarration {
		return sessionNarration{active: active, answered: true}
	}

	for _, tc := range []struct {
		name  string
		narr  sessionNarration
		live  liveness
		want  string
		avoid []string
	}{
		{
			name: "a row present names the driver, whatever liveness says",
			narr: answered(&row), live: liveYes, want: "u-holger",
		},
		{
			// The read FAILED. Nothing is known about the driver, including
			// whether there is one.
			name: "a failed read says the read failed",
			narr: sessionNarration{}, live: liveYes, want: "could not be read",
			avoid: []string{"an unknown driver", "not permitted to see"},
		},
		{
			// Answered, NOTHING AT ALL, and the server says the name IS being
			// driven: the row exists and this caller was scope-filtered out of
			// the entire list.
			name: "answered + wholly empty + live is masked",
			narr: answered(nil), live: liveYes, want: "not permitted to see",
			avoid: []string{"an unknown driver", "could not be read"},
		},
		{
			// THE RACE, and the reason masking needs a third condition.
			//
			// The worker read and the sessions read are separate, so the open
			// session can end between them. This caller sees the worker's ENDED
			// rows and no open one — and is FULLY PERMITTED. Telling them they
			// may not see the driver is a false statement about the reader,
			// manufactured out of a timing accident, and it is the exact shape
			// of the claim PR #504 shipped about a nil holder.
			//
			// Seeing any row at all is the probe: a masked caller is filtered
			// out of the whole list, so `last != nil` is evidence the gate is
			// OPEN. That is cheap and local, and it degrades to "unknown"
			// rather than to a falsehood.
			name: "answered + an ended row + live is a race, not a permissions fact",
			narr: sessionNarration{last: &row, answered: true}, live: liveYes,
			want:  "an unknown driver",
			avoid: []string{"not permitted to see", "could not be read"},
		},
		{
			// Answered, empty, and liveness does not assert a driver. This is
			// the only case the server's own "an unknown driver" wording fits.
			name: "answered + empty + not live is an unknown driver",
			narr: answered(nil), live: liveNo, want: "an unknown driver",
			avoid: []string{"not permitted to see", "could not be read"},
		},
		{
			// Masked LIVENESS is not masked SESSIONS. The caller was refused
			// the worker's working state, so there is no assertion that anybody
			// is driving — inferring a hidden row from an empty list here would
			// invent one.
			name: "answered + empty + masked liveness is an unknown driver",
			narr: answered(nil), live: liveUnknown, want: "an unknown driver",
			avoid: []string{"not permitted to see"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := drivenBy(tc.narr, tc.live)
			if !strings.Contains(got, tc.want) {
				t.Errorf("drivenBy = %q, want it to contain %q", got, tc.want)
			}
			for _, no := range tc.avoid {
				if strings.Contains(got, no) {
					t.Errorf("drivenBy = %q must not claim %q", got, no)
				}
			}
		})
	}
}

// The ORDER of the two nil-explaining arms is load-bearing, and nothing about
// reading them top-to-bottom says so.
//
// A failed read carries no liveness evidence of its own, but `live` is read off
// the WORKER row, which succeeded — so a failed sessions read against a live
// worker satisfies BOTH conditions. If the liveness arm were tested first it
// would win, and every dropped connection during a takeover would be reported
// as a driver the caller is not permitted to see: a confident, specific,
// entirely invented claim about permissions, from a network failure.
func TestAFailedReadIsNotReportedAsMasked(t *testing.T) {
	got := drivenBy(sessionNarration{answered: false}, liveYes)
	if strings.Contains(got, "not permitted") {
		t.Errorf("a failed read must not be reported as a permissions fact: %q", got)
	}
}
