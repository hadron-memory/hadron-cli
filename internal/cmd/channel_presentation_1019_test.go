package cmd

import (
	"os"
	"strings"
	"testing"
)

func TestChannelPresentation1019Disclosure(t *testing.T) {
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"channel", "get", "--help"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile("agentic/agentic-usage.md")
	if err != nil {
		t.Fatal(err)
	}
	for name, text := range map[string]string{"help": out.String(), "agentic": strings.Split(strings.Split(string(body), "# channel get ")[1], "\n")[0]} {
		for _, phrase := range []string{"records a node read", "may decrypt internally"} {
			if !strings.Contains(text, phrase) {
				t.Errorf("%s missing %q", name, phrase)
			}
		}
	}
}

func TestChannelPresentation1019URLRow(t *testing.T) {
	srv, _ := captureGraphQL(t, map[string]string{"GetChannel": `{"data":{"channel":` + channelJSON + `}}`, "NodePortal": `{"data":{"node":{"portalUrl":"https://portal.test/path?a=%2F&b=1"}}}`})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"channel", "get", "caller-ref", "--server", srv.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(out.String(), "\n") {
		if strings.Contains(line, "URL:") {
			if !strings.HasPrefix(line, "URL:") || strings.Join(strings.Fields(line)[1:], " ") != "https://portal.test/path?a=%2F&b=1" {
				t.Fatalf("not a canonical table row: %q", line)
			}
			return
		}
	}
	t.Fatal("missing URL row")
}
