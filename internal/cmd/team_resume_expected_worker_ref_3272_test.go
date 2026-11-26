package cmd

import (
	"strings"
	"testing"
)

// hadron-cli#3272 over hadron-server#5163 (0.48.0, cor:api:140:01): the resume
// input is `expectedWorkerRef`. The server dropped `expectedWorkerId` from the
// input with no alias, so a request still carrying it is refused whole.
func TestResumeRequestNamesTheExpectedWorkerAsARef(t *testing.T) {
	dir := teamGitDir(t)
	t.Setenv("HADRON_LAUNCH_GRANT", "")
	writeBinding(t, dir)
	gql, captured := captureGraphQL(t, mismatchStubs("CLAIM"))
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "start", "--server", gql.URL})
	if err := root.Execute(); err == nil {
		t.Fatal("want the mismatch refusal")
	}
	input := resumeInput(t, captured["ResumeTeamSession"])
	if got := input["expectedWorkerRef"]; got != "wkr1" {
		t.Errorf("expectedWorkerRef = %v, want the binding's wkr1", got)
	}
	if _, sent := input["expectedWorkerId"]; sent {
		t.Error("expectedWorkerId was removed from the input and must not be sent")
	}
}

// The input was renamed; the refusal's extension was not (hadron-server#5314
// keeps `expectedWorkerId`). With the name withheld, the refusal names the
// worker from that extension, an id the binding does not hold.
func TestResumeMismatchStillDecodesTheExpectedWorkerIDExtension(t *testing.T) {
	_, err := runMismatch(t, `"picked":false,"expectedWorkerId":"wkr-from-extension","expectedWorkerName":null,"foundWorkerId":"wkr9","foundWorkerName":"Greer","foundSource":"CLAIM"`, false)
	if err == nil || !strings.Contains(err.Error(), "worker wkr-from-extension,") {
		t.Errorf("the refusal must name the extension's worker id: %v", err)
	}
}

// The help and the agent contract name the input by its new name. The one
// remaining mention of the old spelling is the refusal's extension keys.
func TestResumeTextNamesTheExpectedWorkerRefInput(t *testing.T) {
	for _, args := range [][]string{{"agentic-usage"}, {"team", "session", "start", "--help"}} {
		f, out := testFactory(t)
		root := NewRootCmd(f)
		root.SetOut(out)
		root.SetArgs(args)
		if err := root.Execute(); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		text := out.String()
		if args[0] == "agentic-usage" && !strings.Contains(text, "`expectedWorkerRef`") {
			t.Error("agentic-usage must name the expectedWorkerRef input")
		}
		if args[0] != "agentic-usage" && !strings.Contains(text, "0.48.0") {
			t.Error("the help must state the server floor")
		}
		if rest := strings.ReplaceAll(text, "`expectedWorkerId/Name`", ""); strings.Contains(rest, "expectedWorkerId") {
			t.Errorf("%v still names the old input field", args)
		}
	}
}
