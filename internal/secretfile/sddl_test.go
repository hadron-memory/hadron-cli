package secretfile

import "testing"

func TestOwnerOnlySDDL(t *testing.T) {
	got := ownerOnlySDDL("S-1-5-21-1-2-3-1001")
	want := "D:P(A;;FA;;;S-1-5-21-1-2-3-1001)(A;;FA;;;SY)"
	if got != want {
		t.Fatalf("ownerOnlySDDL = %q, want %q (protected DACL, current user + SYSTEM only)", got, want)
	}
}
