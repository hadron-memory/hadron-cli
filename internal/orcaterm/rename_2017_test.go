package orcaterm

import (
	"context"
	"reflect"
	"testing"
)

// cli#2017: the list reports each tab's id and current title, and a rename is
// one fixed call whose title can never be read as a flag.
func TestTerminalListReportsTabAndTitle2017(t *testing.T) {
	var got []string
	c := NewClient(func(_ context.Context, argv []string) Result {
		got = argv
		return Result{Stdout: `{"ok":true,"result":{"terminals":[{"handle":"h1","worktreePath":"/wt","tabId":"tab1","title":"Iris SHED"}]}}`}
	})
	ts, err := c.List(context.Background())
	if err != nil || len(ts) != 1 || ts[0].TabID != "tab1" || ts[0].Title != "Iris SHED" {
		t.Fatalf("%+v %v", ts, err)
	}
	if !reflect.DeepEqual(got, []string{"terminal", "list", "--json"}) {
		t.Fatalf("argv %v", got)
	}
}

func TestRenameTabCall2017(t *testing.T) {
	var got []string
	ok := func(_ context.Context, argv []string) Result {
		got = argv
		return Result{Stdout: `{"ok":true,"result":{}}`}
	}
	if err := RenameTab(context.Background(), ok, "h1", "-x"); err != nil {
		t.Fatal(err)
	}
	if want := []string{"terminal", "rename", "--terminal", "h1", "--title=-x", "--json"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("argv %v", got)
	}
	for _, tc := range []struct{ handle, title string }{{"", "Iris"}, {"h1", ""}} {
		got = nil
		if RenameTab(context.Background(), ok, tc.handle, tc.title) == nil || got != nil {
			t.Fatalf("%q/%q: an empty handle or title must be refused before any call (an empty title would reset the tab)", tc.handle, tc.title)
		}
	}
	for _, res := range []Result{{Stdout: `{"ok":false,"error":{"code":"terminal_handle_stale"}}`}, {Stdout: "not json"}, {Code: 1}} {
		if err := RenameTab(context.Background(), func(context.Context, []string) Result { return res }, "h1", "Iris"); err != ErrOrca {
			t.Fatalf("%+v: %v", res, err)
		}
	}
}

// The delivery guard still refuses a rename: only the title pass, holding the
// raw runner, may make that call.
func TestDeliveryGuardStillRefusesRename2017(t *testing.T) {
	called := false
	c := NewClient(func(context.Context, []string) Result { called = true; return Result{} })
	if res := c.run(context.Background(), []string{"terminal", "rename", "--terminal", "h1", "--title=Iris"}); res.Code == 0 || called {
		t.Fatalf("guard let a rename through: %+v", res)
	}
}
