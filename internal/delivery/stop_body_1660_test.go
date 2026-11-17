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
// otherwise read as never typed). Red on main: today's copy says "could not
// be typed" in the present tense with no as-of stamp.
func TestStopDropBodyScopesTheUntypedClaimToThePostTime_1660(t *testing.T) {
	body := stopDropBody("ada", StopDrop{EventID: "e", Worker: "Gil", Level: "HALT", HoldReason: "draft", HeldAt: time.Unix(0, 0)})
	if !strings.Contains(body, "has not been told to halt as of") {
		t.Errorf("the halt claim is not scoped to a time: %q", body)
	}
	if !strings.Contains(body, "was not typed into its session as of") {
		t.Errorf("the typing claim is not scoped to a time: %q", body)
	}
}
