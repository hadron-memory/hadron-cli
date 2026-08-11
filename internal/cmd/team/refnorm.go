package team

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Artifact-ref normalization (#369 D13/D14): the worklog stores ONE canonical
// string per external artifact, and that string is the equality-lookup key of
// the provenance query — so the rule is "emit canonical, accept everything".
//
// Canonical forms:
//
//	pr / issue   owner/repo#371        (GitHub's shared number space; `kind`
//	                                    distinguishes a PR from an issue)
//	commit       owner/repo@<full-or-short-sha>
//
// Accepted inputs per kind: the canonical form, a GitHub URL
// (https://github.com/owner/repo/pull/371, /issues/362, /commit/<sha>), and
// the bare number / bare sha — which need defaultRepo (owner/repo) to
// qualify. Pure function; the caller supplies defaultRepo from the session
// binding or a git-remote lookup.

var (
	repoRE = regexp.MustCompile(`^[A-Za-z0-9._-]+/[A-Za-z0-9._-]+$`)
	shaRE  = regexp.MustCompile(`^[0-9a-f]{7,40}$`)
)

// canonicalRepo validates and case-folds an owner/repo path. GitHub
// repository paths are case-insensitive, so `Acme/Widgets` and `acme/widgets`
// must land on ONE equality key — every composition path (short form, URL,
// default repo, remote) goes through here.
func canonicalRepo(repo string) (string, bool) {
	if !repoRE.MatchString(repo) {
		return "", false
	}
	return strings.ToLower(repo), true
}

// normalizeArtifactRef canonicalizes raw for the given kind (pr | issue |
// commit). The returned number is the artifact number for pr/issue (0 for
// commit). A malformed ref is a loud error naming the accepted forms — never
// a silent pass-through, since a non-canonical stored ref would silently miss
// every future provenance lookup.
func normalizeArtifactRef(kind, raw, defaultRepo string) (canonical string, number int, err error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", 0, fmt.Errorf("empty --%s ref", kind)
	}
	switch kind {
	case "pr", "issue":
		return normalizeNumberedRef(kind, raw, defaultRepo)
	case "commit":
		return normalizeCommitRef(raw, defaultRepo)
	default:
		return "", 0, fmt.Errorf("unknown artifact kind %q", kind)
	}
}

func normalizeNumberedRef(kind, raw, defaultRepo string) (string, int, error) {
	// GitHub URL: https://github.com/owner/repo/(pull|issues)/371[/...]
	if repo, tail, ok := splitGitHubURL(raw); ok {
		part, rest, _ := strings.Cut(tail, "/")
		numStr, _, _ := strings.Cut(rest, "/")
		if (part == "pull" || part == "issues") && numStr != "" {
			if n, err := strconv.Atoi(numStr); err == nil && n > 0 {
				return fmt.Sprintf("%s#%d", repo, n), n, nil
			}
		}
		return "", 0, fmt.Errorf("cannot parse a %s number from %q", kind, raw)
	}
	// Short form: owner/repo#371.
	if rawRepo, numStr, ok := strings.Cut(raw, "#"); ok {
		n, err := strconv.Atoi(numStr)
		repo, repoOK := canonicalRepo(rawRepo)
		if err != nil || n <= 0 || !repoOK {
			return "", 0, fmt.Errorf("%q is not a valid %s ref (want owner/repo#N)", raw, kind)
		}
		return fmt.Sprintf("%s#%d", repo, n), n, nil
	}
	// Bare number: needs a repo to qualify.
	if n, err := strconv.Atoi(raw); err == nil && n > 0 {
		if defaultRepo == "" {
			return "", 0, fmt.Errorf("bare %s number %d needs a repository — pass owner/repo#%d, or record --repo at `session start`", kind, n, n)
		}
		repo, ok := canonicalRepo(defaultRepo)
		if !ok {
			return "", 0, fmt.Errorf("session repo %q is not owner/repo — pass the full ref owner/repo#%d", defaultRepo, n)
		}
		return fmt.Sprintf("%s#%d", repo, n), n, nil
	}
	return "", 0, fmt.Errorf("%q is not a recognized %s ref (accepted: a number, owner/repo#N, or a GitHub URL)", raw, kind)
}

func normalizeCommitRef(raw, defaultRepo string) (string, int, error) {
	// GitHub URL: https://github.com/owner/repo/commit/<sha>[/...]
	if repo, tail, ok := splitGitHubURL(raw); ok {
		part, rest, _ := strings.Cut(tail, "/")
		sha, _, _ := strings.Cut(rest, "/")
		if part == "commit" && shaRE.MatchString(strings.ToLower(sha)) {
			return repo + "@" + strings.ToLower(sha), 0, nil
		}
		return "", 0, fmt.Errorf("cannot parse a commit sha from %q", raw)
	}
	// Short form: owner/repo@sha.
	if rawRepo, sha, ok := strings.Cut(raw, "@"); ok {
		sha = strings.ToLower(sha)
		repo, repoOK := canonicalRepo(rawRepo)
		if !repoOK || !shaRE.MatchString(sha) {
			return "", 0, fmt.Errorf("%q is not a valid commit ref (want owner/repo@sha)", raw)
		}
		return repo + "@" + sha, 0, nil
	}
	// Bare sha: needs a repo to qualify.
	if sha := strings.ToLower(raw); shaRE.MatchString(sha) {
		if defaultRepo == "" {
			return "", 0, fmt.Errorf("bare commit sha %s needs a repository — pass owner/repo@%s, or record --repo at `session start`", sha, sha)
		}
		repo, ok := canonicalRepo(defaultRepo)
		if !ok {
			return "", 0, fmt.Errorf("session repo %q is not owner/repo — pass the full ref owner/repo@%s", defaultRepo, sha)
		}
		return repo + "@" + sha, 0, nil
	}
	return "", 0, fmt.Errorf("%q is not a recognized commit ref (accepted: a sha, owner/repo@sha, or a GitHub URL)", raw)
}

// splitGitHubURL recognizes https://github.com/owner/repo/<tail> (http and a
// trailing slash tolerated) and returns owner/repo plus the tail after it.
func splitGitHubURL(raw string) (repo, tail string, ok bool) {
	for _, prefix := range []string{"https://github.com/", "http://github.com/", "github.com/"} {
		if strings.HasPrefix(raw, prefix) {
			parts := strings.SplitN(strings.TrimPrefix(raw, prefix), "/", 3)
			if len(parts) == 3 && parts[0] != "" && parts[1] != "" {
				if r, ok := canonicalRepo(parts[0] + "/" + parts[1]); ok {
					return r, parts[2], true
				}
			}
			return "", "", false
		}
	}
	return "", "", false
}

// repoFromRemote extracts owner/repo from a git remote URL — the last resort
// for qualifying a bare number/sha when the binding recorded no repo.
// Recognizes git@github.com:owner/repo(.git) and http(s)://github.com/owner/repo(.git);
// anything else returns "".
func repoFromRemote(remote string) string {
	remote = strings.TrimSpace(remote)
	var path string
	switch {
	case strings.HasPrefix(remote, "git@github.com:"):
		path = strings.TrimPrefix(remote, "git@github.com:")
	case strings.HasPrefix(remote, "ssh://git@github.com/"):
		path = strings.TrimPrefix(remote, "ssh://git@github.com/")
	case strings.HasPrefix(remote, "https://github.com/"):
		path = strings.TrimPrefix(remote, "https://github.com/")
	case strings.HasPrefix(remote, "http://github.com/"):
		path = strings.TrimPrefix(remote, "http://github.com/")
	default:
		return ""
	}
	path = strings.TrimSuffix(strings.TrimSuffix(path, "/"), ".git")
	if r, ok := canonicalRepo(path); ok {
		return r
	}
	return ""
}
