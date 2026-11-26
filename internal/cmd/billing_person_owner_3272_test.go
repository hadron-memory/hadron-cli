package cmd

import "testing"

// hadron-cli#3272's snapshot refresh carries hadron-server#5207, which lets a
// ledger be owned by a person: `userRef` beside `orgRef`. The CLI names only an
// organization (the person half is hadron-cli#3072), so the new key must stay
// off the wire: a server without it refuses a request that carries it, even as
// null.
func TestBillingWritesNeverSendUserRef(t *testing.T) {
	for _, tc := range []struct {
		op, response string
		args         []string
	}{
		{"GrantCredits", grantResponse, []string{"billing", "grant", "acme", "--amount", "5", "--source-type", "t", "--source-id", "i", "--yes"}},
		{"AdjustCredits", `{"data":{"adjustCredits":` + ledgerGrantJSON + `}}`, []string{"billing", "adjust", "acme", "--amount", "5", "--source-type", "t", "--source-id", "i", "--note", "n", "--yes"}},
	} {
		t.Run(tc.op, func(t *testing.T) {
			_, captured, err := runBilling(t, map[string]string{tc.op: tc.response}, tc.args...)
			if err != nil {
				t.Fatalf("execute: %v", err)
			}
			in := billingInputOf(t, captured, tc.op)
			if in["orgRef"] != "acme" {
				t.Errorf("orgRef = %v", in["orgRef"])
			}
			if _, sent := in["userRef"]; sent {
				t.Errorf("userRef must be omitted: %v", in)
			}
		})
	}
}
