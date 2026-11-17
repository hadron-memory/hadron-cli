package cmd

import (
	"strings"
	"testing"
)

// #1663: a RESCINDED/WITHDRAWN tombstone is never rewritten into a supersession
// (cor:spc:010:01). Fake transport only; the 579 stateful corpus is reused.

type tomb1663 struct {
	name, abstract string
	tags           []any
}

var tombs1663 = []tomb1663{
	{"rescinded-lead", "RESCINDED (2026-10-03); do not apply. Historical: y", []any{"spec"}},
	{"rescinded-lead-lower", "rescinded (2026-10-03); do not apply.", []any{"spec"}},
	{"withdrawn-lead-tag", "WITHDRAWN on 2026-08-21 by someone; do not apply.", []any{"spec", "withdrawn"}},
	{"withdrawn-prose-lead", "Withdrawn, never in force: it was never built.", []any{"spec"}},
	{"rescinded-tag-only", "Original summary.", []any{"spec", "rescinded"}},
	{"withdrawn-tag-only", "Original summary.", []any{"spec", "withdrawn"}},
	{"rescinded-tag-matching-superseded-lead-and-edge", "SUPERSEDED (2026-01-01) by srv:sch:030 (N); do not apply. Historical: Old.", []any{"spec", "rescinded", "superseded"}},
}

func mutations1663(c *corpus579) int {
	n := 0
	for _, op := range c.calls {
		if op == "CreateEdge" || op == "UpdateSpecNode" || op == "CreateSpecNode" {
			n++
		}
	}
	return n
}

func TestSpec1663TombstoneRefusedEverywhere(t *testing.T) {
	paths := map[string][]string{
		"by":            {"--by", "srv:sch:030"},
		"by-dry":        {"--by", "srv:sch:030", "--dry-run"},
		"plain":         {"--to", "app:sch:next", "--title", "New rule"},
		"plain-dry":     {"--to", "app:sch:next", "--title", "New rule", "--dry-run"},
		"plain-resumed": {"--title", "N"},
	}
	for _, tb := range tombs1663 {
		for pn, flags := range paths {
			t.Run(tb.name+"/"+pn, func(t *testing.T) {
				srv, c := server579(t)
				c.nodes["old"]["abstract"], c.nodes["old"]["tags"] = tb.abstract, tb.tags
				if pn == "plain-resumed" || strings.HasPrefix(tb.name, "rescinded-tag-matching") {
					edge579(c, "old", "new")
				}
				out, err := run579(t, srv.URL, flags...)
				if err == nil || exitCodeFor(err) != 5 {
					t.Fatalf("want Conflict(5), got err=%v out=%s", err, out)
				}
				if m := mutations1663(c); m != 0 || c.writes != 0 {
					t.Fatalf("wrote %d on refusal: %v", m, c.calls)
				}
				if strings.Contains(out, "would supersede") {
					t.Fatalf("preview leaked: %s", out)
				}
			})
		}
	}
}

func TestSpec1663TombstoneWithFreeTextEdgeRefused(t *testing.T) {
	srv, c := server579(t)
	c.nodes["old"]["abstract"], c.nodes["old"]["tags"] = "RESCINDED (2026-10-03); do not apply.", []any{"spec", "rescinded"}
	edge579(c, "old", "new")
	c.nodes["old"]["outgoingEdges"].([]any)[0].(map[string]any)["target"] = map[string]any{"id": "x", "loc": "superseded-by (rescinded 2026-08-14)", "memoryId": "mem1"}
	for _, f := range [][]string{{"--by", "srv:sch:030"}, {"--title", "N"}} {
		if _, err := run579(t, srv.URL, f...); err == nil || exitCodeFor(err) != 5 || mutations1663(c) != 0 {
			t.Fatalf("%v: err=%v calls=%v", f, err, c.calls)
		}
	}
}

func TestSpec1663TombstoneAppearsBetweenPasses(t *testing.T) {
	srv, c := server579(t)
	reads := 0
	c.onRead = func(op string, c *corpus579) {
		if op == "GetNode" {
			if reads++; reads == 2 { // second pass re-read of the source
				c.nodes["old"]["abstract"], c.nodes["old"]["tags"] = "RESCINDED (2026-10-03); do not apply.", []any{"spec", "rescinded"}
			}
		}
	}
	_, err := run579(t, srv.URL, "--by", "srv:sch:030")
	if err == nil || exitCodeFor(err) != 5 || mutations1663(c) != 0 {
		t.Fatalf("err=%v calls=%v", err, c.calls)
	}
}

// After the edge/replacement landed, a tombstone on the final read: refuse the
// retirement, report the partial state, exit 5, no "nothing was written"/"rerun".
func TestSpec1663FinalReadRace(t *testing.T) {
	for name, flags := range map[string][]string{
		"by":    {"--by", "srv:sch:030"},
		"plain": {"--to", "app:sch:next", "--title", "New rule"},
	} {
		t.Run(name, func(t *testing.T) {
			srv, c := server579(t)
			final := "GetSpecNodeForEdit"
			if name == "plain" {
				final = "GetSpecNodeRaw"
			}
			c.onRead = func(op string, c *corpus579) {
				if op == final {
					c.nodes["old"]["abstract"], c.nodes["old"]["tags"] = "RESCINDED (2026-10-03); do not apply.", []any{"spec", "rescinded"}
				}
			}
			out, err := run579(t, srv.URL, flags...)
			if err == nil || exitCodeFor(err) != 5 {
				t.Fatalf("want exit 5: err=%v out=%s", err, out)
			}
			if c.updateAttempted {
				t.Fatalf("retired a tombstone: %v", c.calls)
			}
			low := strings.ToLower(err.Error())
			if strings.Contains(low, "nothing was written") || strings.Contains(low, "rerun") {
				t.Fatalf("misleading advice: %v", err)
			}
			if name == "by" && !strings.Contains(out, `"status": "created"`) {
				t.Fatalf("landed edge not reported: %s", out)
			}
			if name == "plain" && c.creates != 1 {
				t.Fatalf("replacement not created: %v", c.calls)
			}
			if !strings.Contains(low, "landed") && !strings.Contains(low, "already") {
				t.Fatalf("partial state not stated: %v", err)
			}
			// A rerun gets the early refusal and writes nothing more.
			before := mutations1663(c)
			if _, err2 := run579(t, srv.URL, "--by", "srv:sch:030"); err2 == nil || exitCodeFor(err2) != 5 || mutations1663(c) != before {
				t.Fatalf("rerun: err=%v", err2)
			}
		})
	}
}

// Controls that must stay green.
func TestSpec1663Controls(t *testing.T) {
	t.Run("acl130-legacy-supersession", func(t *testing.T) {
		srv, c := server579(t)
		c.nodes["old"]["abstract"], c.nodes["old"]["tags"] = "RETIRED 2026-09-25: superseded by something.", []any{"spec", "superseded"}
		edge579(c, "old", "new")
		if _, err := run579(t, srv.URL, "--by", "srv:sch:030"); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("historical-rescinded-in-superseded-lead", func(t *testing.T) {
		srv, c := server579(t)
		c.nodes["old"]["abstract"] = "SUPERSEDED (2026-01-01) by srv:sch:030 (N); do not apply. Historical: RESCINDED (2026-05-01) x."
		c.nodes["old"]["tags"] = []any{"spec", "superseded"}
		edge579(c, "old", "new")
		if out, err := run579(t, srv.URL, "--by", "srv:sch:030"); err != nil || c.writes != 0 || !strings.Contains(out, `"retired": true`) {
			t.Fatalf("err=%v writes=%d %s", err, c.writes, out)
		}
	})
	t.Run("superseded-rescinded-lead-still-conflict", func(t *testing.T) {
		srv, c := server579(t)
		c.nodes["old"]["abstract"] = "SUPERSEDED (rescinded 2026-08-14) by x"
		c.nodes["old"]["tags"] = []any{"spec", "superseded"}
		edge579(c, "old", "new")
		if _, err := run579(t, srv.URL, "--by", "srv:sch:030"); err == nil || exitCodeFor(err) != 5 || c.writes != 0 {
			t.Fatalf("err=%v writes=%d", err, c.writes)
		}
	})
	t.Run("legacy-superseded-tag-no-lead", func(t *testing.T) {
		srv, c := server579(t)
		c.nodes["old"]["tags"] = []any{"spec", "superseded"}
		edge579(c, "old", "new")
		if _, err := run579(t, srv.URL, "--by", "srv:sch:030"); err != nil {
			t.Fatal(err)
		}
	})
}
