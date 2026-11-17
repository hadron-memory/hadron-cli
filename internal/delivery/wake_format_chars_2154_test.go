package delivery

import (
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/output"
)

// hadron-cli#2154: the typed wake line folded controls and whitespace but passed
// Unicode FORMAT characters through: bidi overrides (U+202A-E, U+2066-9),
// directional marks (U+200E/F), zero-width characters (U+200B-D, U+2060, U+FEFF)
// and the soft hyphen. They are not escapes and the agent reads the code points
// in logical order, but a human watching the composer sees reordered or hidden
// text, so the line neutralises them the way `team wakes due` does (SafeLine):
// each becomes a separator, runs collapse to one space, the ends are trimmed.

// formatCases are free text carrying format characters, with the line each must
// become. The expected text is spelled out, never derived from the code under test.
var formatCases = []struct{ name, in, want string }{
	{"bidi override", "Pay\u202eevil\u202c now", "Pay evil now"},
	{"right-to-left mark", "a\u200fb", "a b"},
	{"left-to-right mark", "a\u200eb", "a b"},
	{"zero-width space", "Fix\u200bthe\u200brouter", "Fix the router"},
	{"zero-width joiner and non-joiner", "a\u200db\u200cc", "a b c"},
	{"word joiner", "a\u2060b", "a b"},
	{"byte order mark", "\ufeffLead", "Lead"},
	{"byte order mark inside", "Le\ufeffad", "Le ad"},
	{"isolates", "x\u2066y\u2069z", "x y z"},
	{"soft hyphen", "co\u00adoperate", "co operate"},
	{"a run collapses", "a\u200b\u200b\u202e\u202cb", "a b"},
	{"only format characters", "\u202e\u200b\ufeff", ""},
	{"plain text is untouched", "Fix the router (v2) — ünïcode 日本語", "Fix the router (v2) — ünïcode 日本語"},
	{"controls still fold", "a\x1b[31m\nb", "a [31m b"},
}

func TestTheTypedWakeLineNeutralisesFormatCharactersInATitle2154(t *testing.T) {
	for _, c := range formatCases {
		t.Run(c.name, func(t *testing.T) {
			got := RenderWake([]WakeReason{task("ev1", 0, c.in)})
			want := "[hadron task] assigned: " + c.want + " (task-ev1)"
			if got != want {
				t.Fatalf("title %q rendered %q, want %q", c.in, got, want)
			}
		})
	}
}

func TestTheTypedWakeLineNeutralisesFormatCharactersInATickerName2154(t *testing.T) {
	for _, c := range formatCases {
		t.Run(c.name, func(t *testing.T) {
			r := tick("tk1", tickAt)
			r.TickerName = c.in
			got := RenderWake([]WakeReason{r})
			want := "[hadron tick] " + c.want + ": scheduled check-in. Read your team chat and tasks."
			if c.want == "" {
				want = "[hadron tick] : scheduled check-in. Read your team chat and tasks."
			}
			if got != want {
				t.Fatalf("ticker name %q rendered %q, want %q", c.in, got, want)
			}
		})
	}
}

// Every other free field on the line goes through the same fold: the event
// label (an unknown future token), the handle, the id and a task-less alarm's type.
func TestTheOtherTaskFieldsOfTheTypedLineAreNeutralisedToo2154(t *testing.T) {
	r := WakeReason{Kind: WakeTask, Task: &TaskReason{EventID: "ev1", Event: "FUT\u202eURE", ID: "id\u200b-1", Handle: "AT\u2060.1", Title: "T"}}
	if got, want := RenderWake([]WakeReason{r}), "[hadron task] FUT URE: T (AT .1, id -1)"; got != want {
		t.Fatalf("rendered %q, want %q", got, want)
	}
	alarm := WakeReason{Kind: WakeTask, Task: &TaskReason{EventID: "ev2", Event: "alarm", AlarmType: "delivery\u202e.stuck"}}
	if got, want := RenderWake([]WakeReason{alarm}), "[hadron task] alarm: delivery .stuck (no task)"; got != want {
		t.Fatalf("alarm rendered %q, want %q", got, want)
	}
}

// What reaches the composer is the neutralised line, exactly: through the real
// delivery path, at the boundary where the text is sent.
func TestTheComposerReceivesTheNeutralisedLine2154(t *testing.T) {
	h := newHarness(t)
	reason := task("ev9", 0, "Pay\u202eevil\u202c\u200b now")
	if o := h.deliverWake(h.wake(reason)); o.Kind != Delivered {
		t.Fatalf("outcome %+v", o)
	}
	if len(h.orca.sends) != 1 || h.orca.sends[0].text != "[hadron task] assigned: Pay evil now (task-ev9)" {
		t.Fatalf("sends %+v", h.orca.sends)
	}
}

// What `team wakes due` neutralises, the typed line neutralises too: for every
// rune of a broad sample, if SafeLine turns it into a space then oneLine folds it.
// (oneLine additionally folds ordinary whitespace such as U+00A0, which a table
// may keep, because a typed line is one run of single-spaced words.)
func TestTheTypedLineNeutralisesEverythingTheDueListDoes2154(t *testing.T) {
	for _, r := range []rune{0x00, 0x09, 0x0a, 0x1b, 0x7f, 0x85, 0xad, 0x061c, 0x180e, 0x200b, 0x200c, 0x200d, 0x200e, 0x200f, 0x2028, 0x2029, 0x202a, 0x202b, 0x202c, 0x202d, 0x202e, 0x2060, 0x2066, 0x2067, 0x2068, 0x2069, 0xfeff, 0xfff9, 'a', 'Z', '0', '-', 'é', '日', '🙂'} {
		s := "x" + string(r) + "y"
		if output.SafeLine(s, 100) == "x y" && oneLine(s) != "x y" {
			t.Errorf("U+%04X: the due list neutralises it but the typed line keeps it", r)
		}
		if output.Neutralised(r) != (output.SafeLine(s, 100) == "x y") {
			t.Errorf("U+%04X: output.Neutralised and SafeLine disagree", r)
		}
	}
}
