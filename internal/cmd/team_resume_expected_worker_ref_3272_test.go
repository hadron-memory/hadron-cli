package cmd

import (
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
