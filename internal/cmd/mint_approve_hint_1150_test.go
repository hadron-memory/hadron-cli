package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

// cli#1150: a mint blocked because a node's current revision is not approved
// must say how to approve it. The hint is human output only, printed once
// however many nodes are unapproved; --json stays byte-identical, and a
// report without a NOT_APPROVED blocker prints no hint.

const hint1150 = "approve with: hadron node approve"

const notApprovedBlockers1150 = `[{"kind":"NOT_APPROVED","rule":null,"loc":"pas:010:01","targetLoc":null,"message":"revision 1 is not approved"},` +
	`{"kind":"NOT_APPROVED","rule":null,"loc":"pas:010:02","targetLoc":null,"message":"revision 3 is not approved"},` +
	`{"kind":"PLACEHOLDER","rule":null,"loc":"pas:010:04","targetLoc":null,"message":"reserved, not written"}]`

func nodeMintReply1150(blockers string) string {
	return `{"data":{"mintMemoryNodes":{"memoryId":"m1","dryRun":true,"minted":false,"mintedCount":0,"mintLocs":[],` +
		`"blockers":` + blockers + `,"staleAbstracts":[],"staleAbstractsBlock":false,"openQuestions":[]}}}`
}

func runNodeMint1150(t *testing.T, blockers string, asJSON bool) string {
	t.Helper()
	gql := newVarServer(t, func(op string, _ json.RawMessage) string {
		if op != "MintMemoryNodes" {
			t.Errorf("unexpected operation %s", op)
			return `{}`
		}
		return nodeMintReply1150(blockers)
	})
	f, out := testFactory(t)
	args := []string{"node", "mint", "-m", "acme.com::kb", "--dry-run", "--server", gql.URL}
	if asJSON {
		args = append(args, "--json")
	}
	root := NewRootCmd(f)
	root.SetArgs(args)
	_ = root.Execute() // blocked → exit 5; the output is what is under test
	return out.String()
}

func TestNodeMintNotApprovedPrintsApproveHintOnce(t *testing.T) {
	out := runNodeMint1150(t, notApprovedBlockers1150, false)
	if n := strings.Count(out, hint1150); n != 1 {
		t.Fatalf("want the approve hint exactly once, got %d:\n%s", n, out)
	}
	for _, want := range []string{"--revision", "hadron memory approve-all -m hrn:mem:acme.com:kb"} {
		if !strings.Contains(out, want) {
			t.Errorf("hint must name %q:\n%s", want, out)
		}
	}
}

func TestNodeMintWithoutNotApprovedPrintsNoHint(t *testing.T) {
	out := runNodeMint1150(t, placeholderBlocker, false)
	if strings.Contains(out, "approve") {
		t.Errorf("no NOT_APPROVED blocker, so no approve hint:\n%s", out)
	}
}

func TestNodeMintJSONHasNoHintAndIsUnchanged(t *testing.T) {
	out := runNodeMint1150(t, notApprovedBlockers1150, true)
	if strings.Contains(out, hint1150) {
		t.Errorf("--json stdout must not carry the hint:\n%s", out)
	}
	want := `{
  "memory": "hrn:mem:acme.com:kb",
  "dryRun": true,
  "minted": false,
  "mintedCount": 0,
  "mintLocs": [],
  "blocked": true,
  "blockers": [
    {
      "kind": "NOT_APPROVED",
      "rule": null,
      "loc": "pas:010:01",
      "targetLoc": null,
      "message": "revision 1 is not approved"
    },
    {
      "kind": "NOT_APPROVED",
      "rule": null,
      "loc": "pas:010:02",
      "targetLoc": null,
      "message": "revision 3 is not approved"
    },
    {
      "kind": "PLACEHOLDER",
      "rule": null,
      "loc": "pas:010:04",
      "targetLoc": null,
      "message": "reserved, not written"
    }
  ],
  "staleAbstracts": [],
  "staleAbstractsBlock": false,
  "openQuestions": []
}
`
	if out != want {
		t.Errorf("--json must stay byte-identical to main:\nwant %s\ngot  %s", want, out)
	}
}

func runSpecMint1150(t *testing.T, blockers string, asJSON bool) string {
	t.Helper()
	url, _ := mintServer(t, mintReport(true, false, blockers), mintReport(false, true, `[]`))
	f, out := testFactory(t)
	args := []string{"spec", "mint", "-m", "mem1", "--dry-run", "--server", url}
	if asJSON {
		args = append(args, "--json")
	}
	root := NewRootCmd(f)
	root.SetArgs(args)
	_ = root.Execute()
	return out.String()
}

func TestSpecMintNotApprovedPrintsApproveHintOnce(t *testing.T) {
	out := runSpecMint1150(t, notApprovedBlockers1150, false)
	if n := strings.Count(out, hint1150); n != 1 {
		t.Fatalf("want the approve hint exactly once, got %d:\n%s", n, out)
	}
	if !strings.Contains(out, "--revision") || !strings.Contains(out, "hadron memory approve-all -m") {
		t.Errorf("hint must name node approve --revision and memory approve-all:\n%s", out)
	}
}

func TestSpecMintWithoutNotApprovedPrintsNoHint(t *testing.T) {
	if out := runSpecMint1150(t, placeholderBlocker, false); strings.Contains(out, hint1150) {
		t.Errorf("no NOT_APPROVED blocker, so no approve hint:\n%s", out)
	}
	if out := runSpecMint1150(t, notApprovedBlockers1150, true); strings.Contains(out, hint1150) {
		t.Errorf("--json stdout must not carry the hint:\n%s", out)
	}
}

func TestSpecGroupHelpNamesNodeApprove(t *testing.T) {
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "--help"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "hadron node approve") {
		t.Errorf("spec group help must say where approval happens (hadron node approve):\n%s", out.String())
	}
}
