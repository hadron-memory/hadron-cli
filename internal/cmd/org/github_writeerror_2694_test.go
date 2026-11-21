package org

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/vektah/gqlparser/v2/gqlerror"

	"github.com/hadron-memory/hadron-cli/internal/api"
)

// cli#2694 D4: githubAppWriteError guarded only secret == nil, so an empty
// *secret made strings.Contains(code, *secret) trivially true and withheld the
// server code on every failure, though an empty string can appear in no
// diagnostic. Empty must behave exactly like nil: the mapped server message
// and the server code both survive. (Unreachable through set-app itself —
// cmdutil.ReadSecretInput refuses an empty key — so the function's own
// contract is pinned here at unit level.)
func TestGithubAppWriteErrorEmptySecretKeepsCode2694(t *testing.T) {
	coded := func(code string) error {
		return gqlerror.List{{
			Message:    "org github write refused",
			Extensions: map[string]any{"code": code},
		}}
	}
	empty := ""
	for _, tc := range []struct {
		name   string
		secret *string
	}{
		{"nil secret", nil},
		{"empty secret", &empty},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := githubAppWriteError(coded("ORG_GITHUB_FORBIDDEN"), tc.secret)
			var withCode interface{ ServerCode() string }
			if !errors.As(got, &withCode) || withCode.ServerCode() != "ORG_GITHUB_FORBIDDEN" {
				t.Fatalf("server code lost: %v", got)
			}
			if !strings.Contains(got.Error(), "org github write refused") {
				t.Fatalf("mapped server message lost: %v", got)
			}
		})
	}

	// The genuine-withholding contract stays: a key the code or message
	// actually reflects is still scrubbed (raw error unreachable in the chain,
	// rendered diagnostic free of the key), and a key neither reflects still
	// keeps the code. Each reflection path is exercised ON ITS OWN (cli#2807):
	// one fixture embedding the key in both could not tell which path a
	// regression had stopped scrubbing.
	key := "ghp_secretkey123"
	// assertScrubbed is what every reflection path shares: the key and the
	// server's raw message appear nowhere in the rendered diagnostic, and the
	// raw GraphQL error is not reachable in the chain. The chain check targets
	// the list's MEMBER: errors.Is against the gqlerror.List itself can never
	// match (a slice is not comparable; List.Is compares its members), so a
	// list target would pass whatever the code retained.
	assertScrubbed := func(t *testing.T, got error, raw gqlerror.List) {
		t.Helper()
		if errors.Is(got, raw[0]) {
			t.Fatal("raw GraphQL error reachable in the chain; its message would surface the key")
		}
		var list gqlerror.List
		if errors.As(got, &list) {
			t.Fatal("the raw gqlerror.List is reachable in the chain")
		}
		if strings.Contains(got.Error(), key) {
			t.Fatalf("rendered diagnostic leaks the key: %q", got.Error())
		}
		if strings.Contains(got.Error(), "org github write refused") {
			t.Fatalf("raw server message rides along: %q", got.Error())
		}
	}
	t.Run("only the code embeds the key", func(t *testing.T) {
		raw := gqlerror.List{{
			Message:    "org github write refused",
			Extensions: map[string]any{"code": "E-" + key},
		}}
		got := githubAppWriteError(raw, &key)
		var withCode interface{ ServerCode() string }
		if errors.As(got, &withCode) {
			t.Fatalf("server code %q rides along while embedding the key", withCode.ServerCode())
		}
		if api.ServerCodeOf(got) != "" {
			t.Fatal("the code that embeds the key is reachable through the chain")
		}
		assertScrubbed(t, got, raw)
	})
	t.Run("only the message embeds the key", func(t *testing.T) {
		raw := gqlerror.List{{
			Message:    "org github write refused: " + key,
			Extensions: map[string]any{"code": "ORG_GITHUB_FORBIDDEN"},
		}}
		got := githubAppWriteError(raw, &key)
		// The code is safe, so the wrapper keeps it (ServerCode); the message is the part
		// that must not survive, and the raw chain, whose code ServerCodeOf would read, is gone.
		var withCode interface{ ServerCode() string }
		if !errors.As(got, &withCode) || withCode.ServerCode() != "ORG_GITHUB_FORBIDDEN" {
			t.Fatalf("a code that does not embed the key is kept: %v", got)
		}
		if api.ServerCodeOf(got) != "" {
			t.Fatal("raw error reachable in the chain; its message would surface the key")
		}
		assertScrubbed(t, got, raw)
	})
	t.Run("code does not embed the key", func(t *testing.T) {
		got := githubAppWriteError(coded("ORG_GITHUB_FORBIDDEN"), &key)
		var withCode interface{ ServerCode() string }
		if !errors.As(got, &withCode) || withCode.ServerCode() != "ORG_GITHUB_FORBIDDEN" {
			t.Fatalf("server code lost though the key is not in it: %v", got)
		}
	})
}

// cli#2807: the chain guard above targets a member of the list. Pin that it CAN fire, so it
// cannot go vacuous again: a wrapper that retains the raw list is reachable through its
// member, while the list itself is never a match (the dead form the guard used to take).
func TestRawChainGuardTargetsTheListMember2807(t *testing.T) {
	raw := gqlerror.List{{Message: "org github write refused"}}
	retained := fmt.Errorf("retained: %w", raw)
	if !errors.Is(retained, raw[0]) {
		t.Fatal("a retained list must be reachable through its member: the guard can fire")
	}
	var list gqlerror.List
	if !errors.As(retained, &list) {
		t.Fatal("a retained list must be reachable through errors.As: the guard can fire")
	}
	if errors.Is(retained, raw) {
		t.Fatal("errors.Is against the list itself matched: the premise of #2807 (it never can) changed")
	}
}
