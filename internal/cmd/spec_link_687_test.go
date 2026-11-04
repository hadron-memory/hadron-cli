package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// ---- spec link: truthful on a re-run, a race and a denial (#687 slice 2) ----
//
// `spec link` is the repair the remedies name (supersede, spec lint), so a
// second run must not stack a duplicate edge, and its success must mean the
// edge exists.

// linkedSpecDetail is linkSpecDetail already holding an outgoing edge to the
// target (both endpoints fetch as sp1), named label.
func linkedSpecDetail(edgeID, label string) string {
	edge := `{"id":"` + edgeID + `","name":` + jsonStr(label) + `,"loc":"cor:dmo:020:04:x:cor:dmo:060:02","isRunnable":false,` +
		`"priority":0,"target":{"id":"sp1","loc":"cor:dmo:060:02","memoryId":"mem1"}}`
	return strings.Replace(linkSpecDetail, `"outgoingEdges":[]`, `"outgoingEdges":[`+edge+`]`, 1)
}

const defaultNodeLinkLabel = "documents Node on the Node entity"

type linkDTO687 struct {
	EdgeID        string `json:"edgeId"`
	AlreadyLinked bool   `json:"alreadyLinked"`
	DryRun        bool   `json:"dryRun"`
}

func TestSpecLinkAlreadyLinkedWritesNothing(t *testing.T) {
	// CreateEdge is not mocked: a second write would be an unexpected operation.
	gql, captured := captureGraphQL(t, map[string]string{
		"ResolveUrn": resolveSpecJSON,
		"GetNode":    linkedSpecDetail("le0", defaultNodeLinkLabel),
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "link", "cor:dmo:020:04", "cor:dmo:060:02", "-m", specMem, "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("a re-run of an existing link must succeed: %v", err)
	}
	if _, ok := captured["CreateEdge"]; ok {
		t.Fatal("an existing link must not be written again")
	}
	var dto linkDTO687
	if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
		t.Fatalf("output not JSON: %v\n%s", err, out.String())
	}
	if !dto.AlreadyLinked || dto.EdgeID != "le0" {
		t.Errorf("want alreadyLinked with the existing edge le0, got %+v", dto)
	}
}

func TestSpecLinkAlreadyLinkedTextSaysSo(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"ResolveUrn": resolveSpecJSON,
		"GetNode":    linkedSpecDetail("le0", "documents the nodeType field of Node"),
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "link", "cor:dmo:020:04", "cor:dmo:060:02", "-m", specMem,
		"--label", "documents the nodeType field of Node", "--dry-run", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out.String(), "already linked") {
		t.Errorf("a dry run over an existing link should say it is already linked:\n%s", out.String())
	}
}

func TestSpecLinkOtherLabelStillLinks(t *testing.T) {
	// An edge to the same target under a different label is a different edge.
	gql, captured := captureGraphQL(t, map[string]string{
		"ResolveUrn": resolveSpecJSON,
		"GetNode":    linkedSpecDetail("le0", "some other relation"),
		"CreateEdge": linkEdgeResp,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "link", "cor:dmo:020:04", "cor:dmo:060:02", "-m", specMem, "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if _, ok := captured["CreateEdge"]; !ok {
		t.Fatal("a link under a new label must be written")
	}
	var dto linkDTO687
	_ = json.Unmarshal([]byte(out.String()), &dto)
	if dto.AlreadyLinked || dto.EdgeID != "le1" {
		t.Errorf("want the new edge le1, got %+v", dto)
	}
}

func TestSpecLinkLostRaceReportsTheExistingEdge(t *testing.T) {
	// Another writer created the same edge between the read and the write; the
	// server refuses the duplicate loc with an opaque error. The re-read shows
	// the edge exists, so the link is reported, not the error.
	gql, captured := queueGraphQL(t, map[string][]string{
		"ResolveUrn": {resolveSpecJSON},
		"GetNode":    {linkSpecDetail, linkSpecDetail, linkedSpecDetail("le9", defaultNodeLinkLabel)},
		"CreateEdge": {`{"errors":[{"message":"Unexpected error.","extensions":{"code":"INTERNAL_SERVER_ERROR"}}]}`},
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "link", "cor:dmo:020:04", "cor:dmo:060:02", "-m", specMem, "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("the edge exists after the race, so the link should succeed: %v", err)
	}
	if len(captured["GetNode"]) != 3 {
		t.Errorf("want a re-read after the failed write, got %d GetNode calls", len(captured["GetNode"]))
	}
	var dto linkDTO687
	_ = json.Unmarshal([]byte(out.String()), &dto)
	if !dto.AlreadyLinked || dto.EdgeID != "le9" {
		t.Errorf("want the edge found on re-read, got %+v", dto)
	}
}

func TestSpecLinkDeniedReportsTheDenial(t *testing.T) {
	// A caller without write access to the source memory: the mapped error,
	// non-zero, nothing claimed. The re-read finds no edge.
	gql, _ := queueGraphQL(t, map[string][]string{
		"ResolveUrn": {resolveSpecJSON},
		"GetNode":    {linkSpecDetail},
		"CreateEdge": {`{"errors":[{"message":"Forbidden","extensions":{"code":"FORBIDDEN"}}]}`},
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "link", "cor:dmo:020:04", "cor:dmo:060:02", "-m", specMem, "--json", "--server", gql.URL})
	err := root.Execute()
	if got := exitCodeFor(err); got != exitcode.Forbidden {
		t.Fatalf("a denied link should exit Forbidden, got %d (%v)", got, err)
	}
	if strings.Contains(out.String(), "edgeId") || strings.Contains(out.String(), "linked") {
		t.Errorf("a denied link must claim nothing:\n%s", out.String())
	}
}
