package team

import "testing"

// The normalizer's table test (#369 D13/D14): the canonical string is the
// worklog's equality-lookup key, so every accepted spelling must land on
// exactly one output — and malformed input must error, never pass through.
func TestNormalizeArtifactRef(t *testing.T) {
	cases := []struct {
		name        string
		kind        string
		raw         string
		defaultRepo string
		want        string
		wantNumber  int
		wantErr     bool
	}{
		{name: "pr bare number with repo", kind: "pr", raw: "371", defaultRepo: "hadron-memory/hadron-cli", want: "hadron-memory/hadron-cli#371", wantNumber: 371},
		{name: "pr bare number without repo", kind: "pr", raw: "371", wantErr: true},
		{name: "pr short form", kind: "pr", raw: "hadron-memory/hadron-cli#371", want: "hadron-memory/hadron-cli#371", wantNumber: 371},
		{name: "pr short form ignores defaultRepo", kind: "pr", raw: "acme/widgets#7", defaultRepo: "hadron-memory/hadron-cli", want: "acme/widgets#7", wantNumber: 7},
		{name: "pr url", kind: "pr", raw: "https://github.com/hadron-memory/hadron-cli/pull/371", want: "hadron-memory/hadron-cli#371", wantNumber: 371},
		{name: "pr url with tail", kind: "pr", raw: "https://github.com/hadron-memory/hadron-cli/pull/371/files", want: "hadron-memory/hadron-cli#371", wantNumber: 371},
		{name: "pr url schemeless", kind: "pr", raw: "github.com/hadron-memory/hadron-cli/pull/371", want: "hadron-memory/hadron-cli#371", wantNumber: 371},
		// GitHub repo paths are case-insensitive — every spelling must fold
		// onto ONE equality key.
		{name: "pr short form case-folded", kind: "pr", raw: "Acme/Widgets#7", want: "acme/widgets#7", wantNumber: 7},
		{name: "pr url case-folded", kind: "pr", raw: "https://github.com/Acme/Widgets/pull/7", want: "acme/widgets#7", wantNumber: 7},
		{name: "pr bare number default repo case-folded", kind: "pr", raw: "7", defaultRepo: "Acme/Widgets", want: "acme/widgets#7", wantNumber: 7},
		{name: "commit short form repo case-folded", kind: "commit", raw: "Acme/Widgets@DEADBEEF00", want: "acme/widgets@deadbeef00"},
		{name: "issue url", kind: "issue", raw: "https://github.com/hadron-memory/hadron-cli/issues/362", want: "hadron-memory/hadron-cli#362", wantNumber: 362},
		{name: "issue short form", kind: "issue", raw: "acme/widgets#12", want: "acme/widgets#12", wantNumber: 12},
		{name: "pr zero rejected", kind: "pr", raw: "0", defaultRepo: "a/b", wantErr: true},
		{name: "pr negative rejected", kind: "pr", raw: "a/b#-3", wantErr: true},
		{name: "pr garbage rejected", kind: "pr", raw: "not-a-ref", wantErr: true},
		{name: "pr commit url rejected", kind: "pr", raw: "https://github.com/a/b/commit/abcdef1", wantErr: true},
		{name: "pr malformed repo rejected", kind: "pr", raw: "just-a-repo#5", wantErr: true},
		{name: "commit bare sha with repo", kind: "commit", raw: "93200b2", defaultRepo: "hadron-memory/hadron-cli", want: "hadron-memory/hadron-cli@93200b2"},
		{name: "commit bare sha without repo", kind: "commit", raw: "93200b2", wantErr: true},
		{name: "commit short form", kind: "commit", raw: "acme/widgets@deadbeef00", want: "acme/widgets@deadbeef00"},
		{name: "commit uppercase sha lowered", kind: "commit", raw: "acme/widgets@DEADBEEF00", want: "acme/widgets@deadbeef00"},
		{name: "commit url", kind: "commit", raw: "https://github.com/acme/widgets/commit/93200b296ec1e9ae287082744b68fc42efb3277a", want: "acme/widgets@93200b296ec1e9ae287082744b68fc42efb3277a"},
		{name: "commit sha too short", kind: "commit", raw: "abc123", defaultRepo: "a/b", wantErr: true},
		{name: "commit non-hex rejected", kind: "commit", raw: "a/b@notahexsha", wantErr: true},
		{name: "branch short form", kind: "branch", raw: "acme/widgets:main", want: "acme/widgets:main"},
		// Branch names are case-sensitive and may contain slashes; only the
		// repo part folds. A bare value is ALWAYS a branch name, never
		// owner/repo — feature/foo is a common branch shape.
		{name: "branch case: repo folds, branch verbatim", kind: "branch", raw: "Acme/Widgets:Feature-X", want: "acme/widgets:Feature-X"},
		{name: "branch bare with repo", kind: "branch", raw: "team-chat", defaultRepo: "hadron-memory/hadron-cli", want: "hadron-memory/hadron-cli:team-chat"},
		{name: "branch bare slashed with repo", kind: "branch", raw: "feature/foo", defaultRepo: "a/b", want: "a/b:feature/foo"},
		{name: "branch bare without repo", kind: "branch", raw: "main", wantErr: true},
		{name: "branch url", kind: "branch", raw: "https://github.com/acme/widgets/tree/feature/foo", want: "acme/widgets:feature/foo"},
		{name: "branch with space rejected", kind: "branch", raw: "a/b:not a branch", wantErr: true},
		{name: "unknown kind", kind: "tag", raw: "a/b:v1", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, n, err := normalizeArtifactRef(tc.kind, tc.raw, tc.defaultRepo)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("want error, got %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want || n != tc.wantNumber {
				t.Errorf("got (%q, %d), want (%q, %d)", got, n, tc.want, tc.wantNumber)
			}
		})
	}
}

func TestRepoFromRemote(t *testing.T) {
	cases := map[string]string{
		"git@github.com:Acme/Widgets.git":                 "acme/widgets",
		"git@github.com:hadron-memory/hadron-cli.git":     "hadron-memory/hadron-cli",
		"git@github.com:hadron-memory/hadron-cli":         "hadron-memory/hadron-cli",
		"ssh://git@github.com/hadron-memory/hadron-cli":   "hadron-memory/hadron-cli",
		"https://github.com/hadron-memory/hadron-cli.git": "hadron-memory/hadron-cli",
		"https://github.com/hadron-memory/hadron-cli/":    "hadron-memory/hadron-cli",
		"https://gitlab.com/acme/widgets.git":             "",
		"not a remote":                                    "",
	}
	for remote, want := range cases {
		if got := repoFromRemote(remote); got != want {
			t.Errorf("repoFromRemote(%q) = %q, want %q", remote, got, want)
		}
	}
}
