package org

import (
	"errors"
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

	// The genuine-withholding contract stays: a key the code actually reflects
	// is still scrubbed (code dropped, raw error unreachable in the chain),
	// and a key the code does not reflect still keeps the code.
	key := "ghp_secretkey123"
	t.Run("code embeds the key", func(t *testing.T) {
		got := githubAppWriteError(coded("E-"+key), &key)
		var withCode interface{ ServerCode() string }
		if errors.As(got, &withCode) {
			t.Fatalf("server code %q rides along while embedding the key", withCode.ServerCode())
		}
		if api.ServerCodeOf(got) != "" {
			t.Fatal("raw error reachable in the chain; its code would surface the key")
		}
	})
	t.Run("code does not embed the key", func(t *testing.T) {
		got := githubAppWriteError(coded("ORG_GITHUB_FORBIDDEN"), &key)
		var withCode interface{ ServerCode() string }
		if !errors.As(got, &withCode) || withCode.ServerCode() != "ORG_GITHUB_FORBIDDEN" {
			t.Fatalf("server code lost though the key is not in it: %v", got)
		}
	})
}
