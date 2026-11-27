package api

import (
	"strings"
	"testing"

	"github.com/vektah/gqlparser/v2/gqlerror"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// hadron-cli#3310 (companion of hadron-server#5394, cor:cht:010:02): startChat refuses
// CHAT_APP_NOT_IDENTIFIED unless the call identifies the App, by an App credential or by
// the new optional appRef. The caller fixes the input (it names the App), so the refusal
// is a usage error, exit 2, and the server's message, which says how, is kept.
const chatAppNotIdentified3310 = "startChat needs to know which App supplies this chat: use an App credential, or name the App (appRef; the CLI's global --app)."

func TestChatAppNotIdentifiedIsAUsageErrorWithTheServersMessage3310(t *testing.T) {
	err := gqlerror.List{{Message: chatAppNotIdentified3310, Extensions: map[string]any{"code": "CHAT_APP_NOT_IDENTIFIED"}}}
	mapped := MapError(err)
	if got := exitcode.FromError(mapped); got != exitcode.Usage {
		t.Errorf("CHAT_APP_NOT_IDENTIFIED must exit %d (the caller names the App), got %d (%v)", exitcode.Usage, got, mapped)
	}
	if !strings.Contains(mapped.Error(), chatAppNotIdentified3310) {
		t.Errorf("the server's message must be kept, it says how to fix the call:\n got: %v\nwant: %s", mapped, chatAppNotIdentified3310)
	}
	if got := ServerCodeOf(err); got != "CHAT_APP_NOT_IDENTIFIED" {
		t.Errorf("the server code must stay readable for --json: %q", got)
	}
}
