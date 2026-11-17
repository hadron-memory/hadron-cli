package cmd

import (
	"strings"
	"testing"
)

func TestSubscriptionCreateHelp2304(t *testing.T) {
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"memory", "subscription", "create", "--help"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	text := strings.Join(strings.Fields(out.String()), " ")
	for _, want := range []string{"visibility PUBLIC", "ADMIN of the subscribing organization", "owning organization cannot subscribe", "owning organization's admin can change its role or remove it", "organization domain or a user handle"} {
		if !strings.Contains(text, want) {
			t.Errorf("help missing %q: %s", want, text)
		}
	}
}

// The report does not include a full server payload; these typed refusal
// fixtures are synthetic. Hidden and missing memory refusals must stay identical.
func TestSubscriptionCreateRefusals2304(t *testing.T) {
	for _, tc := range []struct {
		name, code, message, want string
		exit                      int
	}{
		{"non-admin", "FORBIDDEN", "Not permitted", "ADMIN of the subscribing organization", 8},
		{"hidden", "NOT_FOUND", "Memory not found", "Memory not found", 4},
		{"missing", "NOT_FOUND", "Memory not found", "Memory not found", 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gql, _ := captureGraphQL(t, map[string]string{"CreateMemorySubscription": `{"errors":[{"message":"` + tc.message + `","extensions":{"code":"` + tc.code + `"}}]}`})
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs([]string{"memory", "subscription", "create", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "--org", "partnerco.com", "--role", "reader", "--server", gql.URL})
			err := root.Execute()
			if err == nil || exitCodeFor(err) != tc.exit || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err=%v exit=%d", err, exitCodeFor(err))
			}
			if tc.code == "NOT_FOUND" && err.Error() != tc.message {
				t.Fatalf("not-found wording changed: %q", err.Error())
			}
		})
	}
}
