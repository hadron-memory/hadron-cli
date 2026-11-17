package team

import (
	"regexp"
	"strings"

	urn "github.com/hadron-memory/urn-lib-go"

	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// bareTaskHandleRe is the task handle grammar (cor:agt:050:01): AT0.7, AT1.2.3.
var bareTaskHandleRe = regexp.MustCompile(`^AT\d+(\.\d+)*$`)

// qualifyTaskRef is the ONE place every `team task` site names a task through
// (cli#1831, cli#1656 row 14), so an explicit --app is never accepted and then
// ignored again.
//
// With no --app the ref is returned exactly as given: the configured App
// context and the worktree binding never qualify a handle, so a bare handle
// still reaches the server and gets its own TASK_HANDLE_QUALIFY answer.
//
// With --app:
//   - a bare handle is composed into hrn:agenttask:<root>:<slug>:<handle>. A
//     qualified handle never reaches past the caller's access to the App it
//     names, so this adds no reach, and the server's count-based bare-handle
//     rule is untouched;
//   - an App id cannot be composed without a read, so a bare handle with one
//     is refused (exit 2, nothing sent);
//   - an hrn:agenttask: ref naming another App is refused (exit 2, nothing
//     sent); naming the same App, it is sent unchanged;
//   - anything else (a task id, a non-handle) is sent unchanged.
func qualifyTaskRef(f *cmdutil.Factory, ref string) (string, error) {
	if f.AppFlag == "" {
		return ref, nil
	}
	app, err := cmdutil.CanonicalAppRef("--app", f.AppFlag)
	if err != nil {
		return "", err
	}
	root, slug, isURN := cmdutil.AppParts(app)
	trimmed := strings.TrimSpace(ref)
	if bareTaskHandleRe.MatchString(trimmed) {
		if !isURN {
			return "", exitcode.Newf(exitcode.Usage,
				"--app %s is an App id, and qualifying the bare handle %s needs the App's URN: pass --app hrn:app:<root>:<slug>, or name the task as hrn:agenttask:<root>:<slug>:%s",
				app, trimmed, trimmed)
		}
		qualified, err := urn.ComposeUrnV2("agenttask", root, slug, trimmed)
		if err != nil {
			return "", exitcode.Newf(exitcode.Usage, "cannot qualify %s in --app %s: %v", trimmed, app, err)
		}
		return qualified, nil
	}
	if isURN {
		if r, s, ok := taskRefApp(trimmed); ok && (r != root || s != slug) {
			return "", exitcode.Newf(exitcode.Usage,
				"%s names a task in App hrn:app:%s:%s, and --app is %s: omit --app, or name the task in that App",
				trimmed, r, s, app)
		}
	}
	return ref, nil
}

// taskRefApp reads the App out of a qualified task ref, in the flat form
// (hrn:agenttask:<root>:<slug>:<handle>) or the server's embedded-App form
// (hrn:agenttask:hrn:app:<root>:<slug>:<handle>). ok is false for anything
// else, which is then sent unchanged for the server to answer.
func taskRefApp(ref string) (root, slug string, ok bool) {
	body, found := strings.CutPrefix(urn.NormalizeScheme(ref), "hrn:agenttask:")
	if !found {
		return "", "", false
	}
	body = strings.TrimPrefix(urn.NormalizeScheme(body), "hrn:app:")
	parts := strings.SplitN(body, ":", 3)
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}
