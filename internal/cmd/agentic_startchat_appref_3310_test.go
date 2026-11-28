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
	// The distinctive sentences, so deleting the paragraph cannot leave the test green.
	for _, want := range []string{
		"`startChat` through `hadron api` with a person's token must name the App with the `appRef` argument",
		"`CHAT_APP_NOT_IDENTIFIED` by a server from 0.54.0",
		"`hadron api` prints that refusal verbatim and exits 2 on it",
	} {
		if !strings.Contains(u, want) {
			t.Errorf("agentic-usage must tell a `hadron api` caller of startChat to name the App with appRef; missing %q", want)
		}
	}
	// No curated command sends startChat (gap #64), so the docs must not credit one.
	if strings.Contains(u, "The curated commands map that refusal") {
		t.Error("no curated command sends startChat: hadron api is the path that exits 2")
	}
}

// What `hadron api` actually does with the refusal: it prints the envelope verbatim and, since
// CHAT_APP_NOT_IDENTIFIED is a usage error, exits 2 (it was 1).
func TestHadronAPIExitsUsageOnChatAppNotIdentified3310(t *testing.T) {
	const msg = "startChat needs to know which App supplies this chat: use an App credential, or name the App (appRef; the CLI's global --app)."
	reply := `{"data":null,"errors":[{"message":"` + strings.ReplaceAll(msg, `"`, `\"`) + `","extensions":{"code":"CHAT_APP_NOT_IDENTIFIED"}}]}`
	gql, _ := captureGraphQLFunc(t, func(string) string { return reply })
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"api", `mutation StartChat { startChat(agentRef: "a") { id } }`, "--server", gql.URL})
	err := root.Execute()
	if err == nil {
		t.Fatal("the refusal must fail the command")
	}
	if got := exitCodeFor(err); got != 2 {
		t.Errorf("hadron api must exit 2 (a usage error: name the App) on CHAT_APP_NOT_IDENTIFIED, got %d (%v)", got, err)
	}
	if !strings.Contains(out.String(), msg) || !strings.Contains(out.String(), "CHAT_APP_NOT_IDENTIFIED") {
		t.Errorf("hadron api prints the refusal envelope verbatim:\n%s", out.String())
	}
}
