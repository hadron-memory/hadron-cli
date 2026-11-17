package cmd

import (
	"strings"
	"testing"
	"time"

	"github.com/hadron-memory/hadron-cli/internal/auth"
	"github.com/hadron-memory/hadron-cli/internal/auth/store"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// hadron-cli#1759 row 2 (Riana §10 P3 on #1632): PublicGraphQLClient records
// the credential it really sent, so a public command (`hadron find`) refusing an
// HADRON_TOKEN or impersonation credential never gets the stored-login remedy.
// A stored user login is on the machine in every case. The control is a stored
// login actually sent, which keeps exactly one remedy. Failure messages name
// the case, never a token.
func TestCredentialFollowups1759PublicClientClassifiesSource(t *testing.T) {
	for _, tc := range []struct {
		name     string
		setup    func(t *testing.T, srvURL string) (extra map[string]string, wantBearer string)
		wantHint int
	}{
		{"stored-control", func(t *testing.T, _ string) (map[string]string, string) {
			return nil, "Bearer hdr_user_stored"
		}, 1},
		{"env", func(t *testing.T, _ string) (map[string]string, string) {
			return nil, "Bearer hdr_env_1759"
		}, 0},
		{"impersonation", func(t *testing.T, srvURL string) (map[string]string, string) {
			imp := impToken1624(t, time.Now().Add(time.Hour))
			return map[string]string{auth.ImpersonationHostKey(srvURL): imp}, "Bearer " + imp
		}, 0},
	} {
		for _, asJSON := range []bool{false, true} {
			t.Run(tc.name+map[bool]string{true: "-json"}[asJSON], func(t *testing.T) {
				srv, bearers := refusingServer1624(t, nil)
				f, out := testFactory(t)
				extra, wantBearer := tc.setup(t, srv.URL)
				withStoredLogin1624(t, f, srv.URL, extra)
				if tc.name == "env" {
					t.Setenv(store.EnvToken, "hdr_env_1759")
				}
				a := []string{"find", "anything", "--server", srv.URL}
				if asJSON {
					a = append(a, "--json")
				}
				code, errText := run1624(t, f, a)
				if got := bearers(); len(got) != 1 || got[0] != wantBearer {
					t.Fatalf("want one request carrying the %s credential, got %d request(s) or another credential", tc.name, len(got))
				}
				if code != exitcode.AuthRequired {
					t.Fatalf("exit = %d, want AuthRequired", code)
				}
				if n := strings.Count(outputs1624(out, errText), storedHint1624); n != tc.wantHint {
					t.Errorf("%s: stored-login remedy shown %d time(s), want %d", tc.name, n, tc.wantHint)
				}
			})
		}
	}
}
