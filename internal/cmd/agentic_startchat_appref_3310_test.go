package cmd

import (
	"strings"
	"testing"
)

// hadron-cli#3310: a startChat sent through `hadron api` with a person's token names the
// App with appRef (the global --app does not reach an `hadron api` operation), or the
// server refuses it CHAT_APP_NOT_IDENTIFIED (hadron-server#5394, from server 0.54.0).
func TestAgenticUsageNamesStartChatAppRef3310(t *testing.T) {
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetOut(f.IOStreams.Out)
	root.SetArgs([]string{"agentic-usage"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	u := strings.Join(strings.Fields(out.String()), " ")
	for _, want := range []string{"startChat", "appRef", "CHAT_APP_NOT_IDENTIFIED", "0.54.0"} {
		if !strings.Contains(u, want) {
			t.Errorf("agentic-usage must tell a `hadron api` caller of startChat to name the App with appRef; missing %q", want)
		}
	}
}
