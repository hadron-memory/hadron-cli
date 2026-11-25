package team

import (
	"context"
	"regexp"
	"strings"

	urn "github.com/hadron-memory/urn-lib-go"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// bareTaskHandleRe is the task handle grammar (cor:agt:050:01): AT0.7, AT1.2.3.
var bareTaskHandleRe = regexp.MustCompile(`^AT\d+(\.\d+)*$`)

// qualifyTaskRef is the ONE place every `team task` site names a task through
// (cli#1831, cli#1656 row 14), so an explicit --app is never accepted and then
// ignored again.
//
// With no --app the ref is returned exactly as given (the two reads that also use
// the binding go through qualifyTaskRefFromBinding, cli#3037): the configured App
// context never qualifies a handle, so a bare handle
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
				"--app is an App id, and qualifying the bare task handle needs the App's URN (neither value is shown): pass --app hrn:app:<root>:<slug>, or name the task as hrn:agenttask:<root>:<slug>:<handle>")
		}
		qualified, err := urn.ComposeUrnV2("agenttask", root, slug, trimmed)
		if err != nil {
			return "", exitcode.Newf(exitcode.Usage, "cannot qualify the task handle in --app (neither value is shown)")
		}
		return qualified, nil
	}
	if isURN {
		if r, s, ok := taskRefApp(trimmed); ok && (r != root || s != slug) {
			return "", exitcode.Newf(exitcode.Usage,
				"the task ref names a task in another App than --app (neither value is shown): omit --app, or name the task in that App")
		}
	}
	return ref, nil
}

// qualifyTaskRefFromBinding is qualifyTaskRef for the READS that name a task (`team task get`,
// `team pr readiness`), which also qualify a bare handle from this worktree's session binding when
// there is no --app (cli#3037). The writes keep resolving through the bound worker session they send,
// so they never come here and make no extra request.
//
// The App comes from the binding only: never from the configured App context, because a person with a
// default App still belongs to several (cor:agt:050:01). The binding stores the App's id, so one
// identity read turns it into the URN the qualified handle needs; the read is made only for a bare
// handle in a worktree whose complete binding is for this server. Whatever cannot be established (no
// worktree or binding, another server, a failed or empty lookup, an answer naming a different App than
// the binding) leaves the ref exactly as given, so the server's own count rule answers as it always
// did (TASK_HANDLE_QUALIFY for a caller in several Apps). Qualifying adds no reach: a qualified handle
// never reaches past the caller's access to the App it names.
func qualifyTaskRefFromBinding(ctx context.Context, f *cmdutil.Factory, ref string) (string, error) {
	qualified, err := qualifyTaskRef(f, ref)
	if err != nil || f.AppFlag != "" {
		return qualified, err
	}
	handle := strings.TrimSpace(ref)
	if !bareTaskHandleRe.MatchString(handle) {
		return qualified, nil
	}
	b, _, berr := readBindingFile(ctx)
	if berr != nil || b == nil || b.AppID == "" || b.Server == "" {
		return qualified, nil
	}
	server, serr := f.Server()
	if serr != nil || api.Endpoint(server) != api.Endpoint(b.Server) {
		return qualified, nil
	}
	client, cerr := f.GraphQLClient()
	if cerr != nil {
		return qualified, nil
	}
	resp, lerr := gen.TeamAppIdentity(ctx, client, b.AppID)
	if lerr != nil || resp == nil || resp.App == nil || resp.App.Id != b.AppID {
		return qualified, nil
	}
	root, slug, ok := cmdutil.AppParts(resp.App.Urn)
	if !ok {
		return qualified, nil
	}
	composed, cerr := urn.ComposeUrnV2("agenttask", root, slug, handle)
	if cerr != nil {
		return qualified, nil
	}
	return composed, nil
}

// taskRefParts reads a qualified task ref into its App (root, slug) and its
// handle, in the flat form (hrn:agenttask:<root>:<slug>:<handle>) or the
// server's embedded-App form (hrn:agenttask:hrn:app:<root>:<slug>:<handle>). ok
// is false for anything else, which is then sent unchanged for the server to
// answer.
func taskRefParts(ref string) (root, slug, handle string, ok bool) {
	body, found := strings.CutPrefix(urn.NormalizeScheme(ref), "hrn:agenttask:")
	if !found {
		return "", "", "", false
	}
	body = strings.TrimPrefix(urn.NormalizeScheme(body), "hrn:app:")
	parts := strings.SplitN(body, ":", 3)
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return "", "", "", false
	}
	return parts[0], parts[1], parts[2], true
}

// taskRefApp reads the App out of a qualified task ref (see taskRefParts).
func taskRefApp(ref string) (root, slug string, ok bool) {
	root, slug, _, ok = taskRefParts(ref)
	return root, slug, ok
}
