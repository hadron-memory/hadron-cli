package comment

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

type capture struct {
	calls           int
	verb, ref, body string
	bodyPtr, quote  *string
	revision        int
	anchor          *int
	states          []string
	limit, offset   int
	page            Page
	result          *Comment
	err             error
	reopen          bool
}

func (c *capture) List(_ context.Context, r string, s []string, l, o int) (Page, error) {
	c.calls++
	c.ref = r
	c.states = s
	c.limit = l
	c.offset = o
	return c.page, c.err
}
func (c *capture) Get(_ context.Context, r string) (*Comment, error) {
	c.calls++
	c.ref = r
	return c.result, c.err
}
func (c *capture) Create(_ context.Context, r, b string, q *string, a *int) (Comment, error) {
	c.calls++
	c.verb = "create"
	c.ref = r
	c.body = b
	c.quote = q
	c.anchor = a
	return Comment{}, c.err
}
func (c *capture) Reply(_ context.Context, r, b string) (Comment, error) {
	c.calls++
	c.verb = "reply"
	c.ref = r
	c.body = b
	return Comment{}, c.err
}
func (c *capture) Edit(_ context.Context, r string, b, q *string, v int) (Comment, error) {
	c.calls++
	c.verb = "edit"
	c.ref = r
	c.bodyPtr = b
	c.quote = q
	c.revision = v
	return Comment{}, c.err
}
func (c *capture) Retract(_ context.Context, r string, v int) (Comment, error) {
	c.calls++
	c.verb = "retract"
	c.ref = r
	c.revision = v
	return Comment{}, c.err
}
func (c *capture) Resolve(_ context.Context, r string, v int, reopen bool) (Comment, error) {
	c.calls++
	c.verb = "resolve"
	c.ref = r
	c.revision = v
	c.reopen = reopen
	return Comment{}, c.err
}
func execute(t *testing.T, c *capture, args ...string) (string, int, error) {
	t.Helper()
	streams, _, _ := output.Test()
	var out strings.Builder
	streams.Out = &out
	streams.In = strings.NewReader("body from pipe\n")
	f := &cmdutil.Factory{IOStreams: streams, JSON: true}
	connections := 0
	cmd := NewCmd(f, func(context.Context) (Service, error) { connections++; return c, nil })
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), connections, err
}
func TestInvalidInputsNeverConnect(t *testing.T) {
	for _, args := range [][]string{
		{"create", "target", "-m", "hrn:mem:acme.com:kb"},
		{"create", "target", "-m", "hrn:mem:acme.com:kb", "--body", "  "},
		{"create", "target", "-m", "hrn:mem:acme.com:kb", "--body", "x", "--anchor-revision", "0"},
		{"edit", "comments:one", "-m", "hrn:mem:acme.com:kb", "--body", "x"},
		{"edit", "comments:one", "-m", "hrn:mem:acme.com:kb", "--expected-revision", "1"},
		{"retract", "comments:one", "-m", "hrn:mem:acme.com:kb", "--expected-revision", "-1"},
		{"list", "target", "-m", "hrn:mem:acme.com:kb", "--limit", "0"},
		{"list", "target", "-m", "hrn:mem:acme.com:kb", "--limit", "201"},
		{"list", "target", "-m", "hrn:mem:acme.com:kb", "--state", "INVALID"},
		{"list", "target", "-m", "hrn:mem:acme.com:kb", "--state", ""},
		{"get", "unqualified"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			c := &capture{}
			_, connections, err := execute(t, c, args...)
			if err == nil || connections != 0 || c.calls != 0 {
				t.Fatalf("err=%v connects=%d calls=%d", err, connections, c.calls)
			}
		})
	}
}
func TestEditPreservesOmittedBodyAndClearsQuote(t *testing.T) {
	c := &capture{}
	_, _, err := execute(t, c, "edit", "comments:one", "-m", "hrn:mem:acme.com:kb", "--quote", "", "--expected-revision", "7")
	if err != nil {
		t.Fatal(err)
	}
	if c.bodyPtr != nil || c.quote == nil || *c.quote != "" || c.revision != 7 || c.ref != "hrn:node:acme.com:kb:comments:one" {
		t.Fatalf("request=%+v", c)
	}
}
func TestCreatePipeAndExplicitAnchor(t *testing.T) {
	c := &capture{}
	_, _, err := execute(t, c, "create", "target", "-m", "hrn:mem:acme.com:kb", "--body", "-", "--anchor-revision", "3")
	if err != nil {
		t.Fatal(err)
	}
	if c.body != "body from pipe\n" || c.anchor == nil || *c.anchor != 3 || c.quote != nil {
		t.Fatalf("request=%+v", c)
	}
}
func TestConflictIsReturnedWithoutReadOrRetry(t *testing.T) {
	for _, verb := range []string{"edit", "retract", "resolve"} {
		t.Run(verb, func(t *testing.T) {
			c := &capture{err: exitcode.Newf(exitcode.Conflict, "NODE_WRITE_CONFLICT")}
			args := []string{verb, "comments:one", "-m", "hrn:mem:acme.com:kb", "--expected-revision", "2"}
			if verb == "edit" {
				args = append(args, "--body", "new")
			}
			out, _, err := execute(t, c, args...)
			if exitcode.FromError(err) != 5 || c.calls != 1 || out != "" {
				t.Fatalf("out=%q err=%v calls=%d", out, err, c.calls)
			}
		})
	}
}
func TestListPageAndStubJSON(t *testing.T) {
	c := &capture{page: Page{Items: []Thread{{Root: Comment{Retracted: true, Revision: 4}, ReplyCount: 0}}, Total: 8}}
	out, _, err := execute(t, c, "list", "target", "-m", "hrn:mem:acme.com:kb", "--state", "OPEN,RESOLVED", "--limit", "2", "--offset", "3")
	if err != nil {
		t.Fatal(err)
	}
	if c.limit != 2 || c.offset != 3 || len(c.states) != 2 || c.calls != 1 {
		t.Fatalf("request=%+v", c)
	}
	var d map[string]any
	if err = json.Unmarshal([]byte(out), &d); err != nil {
		t.Fatal(err)
	}
	items := d["items"].([]any)
	thread := items[0].(map[string]any)
	root := thread["root"].(map[string]any)
	if root["body"] != nil || root["quote"] != nil || len(thread["replies"].([]any)) != 0 || d["total"] != float64(8) {
		t.Fatalf("dto=%s", out)
	}
}
func TestEmptyListIsArrayAndMissingCommentIsNotFound(t *testing.T) {
	out, _, err := execute(t, &capture{}, "list", "target", "-m", "hrn:mem:acme.com:kb")
	if err != nil || !strings.Contains(out, `"items": []`) {
		t.Fatalf("out=%s err=%v", out, err)
	}
	_, _, err = execute(t, &capture{}, "get", "comments:one", "-m", "hrn:mem:acme.com:kb")
	if exitcode.FromError(err) != 4 {
		t.Fatal(err)
	}
}
func TestTextDoesNotPrintHiddenBodyAndLabelsOldAnchor(t *testing.T) {
	var w strings.Builder
	body := "not for display"
	c := Comment{URN: "hrn:node:acme.com:kb:comments:one", Body: &body, Hidden: true, Revision: 3, AnchorRevision: 1, AnchorIsCurrent: false}
	if err := render(&w, c); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(w.String(), body) || !strings.Contains(w.String(), "hidden") || !strings.Contains(w.String(), "older target text") {
		t.Fatal(w.String())
	}
}

func TestAuthorLabelKeepsIdentityWithoutDisplayFields(t *testing.T) {
	a := Author{Kind: "WORKER", Worker: &Actor{ID: "w1"}}
	if got := authorLabel(a); got != "w1 (worker)" {
		t.Fatal(got)
	}
}
