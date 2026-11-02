package cmd

import (
	"crypto/sha256"
	"fmt"
	"testing"
)

// Output hashes and create raw variables captured on fresh main29c58a0b.
// The GraphQL document adds selections; unchanged inputs/output are the contract.
func TestTaskMatching1549LegacyParity(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"create json", []string{"create", "example", "--json"}, "5b14c65736d9f48849fff9a6fd38967dadc66b50d5ec48c4ce46de767c90ac5e"},
		{"create human", []string{"create", "example"}, "9f592f1fcb6e727886a55f1d0e9ca674b2d1c76a675e36c89429f2a7b661e6e0"},
		{"get json", []string{"get", "task1", "--json"}, "6a4d52bbe8bf9eae53009f8cb5f18ee7dc563ba99dad249beb2bb88b595883d7"},
		{"get human", []string{"get", "task1"}, "7b142eb9d2927fb33cb0cf711e9200bf4477eceacc34900721f0b9a4993a7d15"},
		{"list json", []string{"list", "--json"}, "2340698fe31127434f13ac3de9e1d680551993288f9abaa65017f4a4c197ebf7"},
		{"list human", []string{"list"}, "71ace79388fbcf77328ea206f6955986da515ac3cba40f6eb95511fdebc59e66"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			calls, out, err := runTeamTask(t, taskResponses(), append([]string{"team", "task"}, tc.args...)...)
			if err != nil {
				t.Fatal(err)
			}
			got := fmt.Sprintf("%x", sha256.Sum256([]byte(out)))
			if got != tc.want {
				t.Errorf("output sha256 %s", got)
			}
			for _, c := range *calls {
				if c.Op == "AgentTaskCreate" {
					if string(c.RawVars) != `{"input":{"appRef":"capp100000000000000000000","closeSessionWhenDone":null,"description":null,"parentTaskId":null,"source":null,"startBy":null,"status":null,"tags":null,"title":"example"}}` {
						t.Fatalf("legacy variables changed: %s", c.RawVars)
					}
				}
			}
		})
	}
}
