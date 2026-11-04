package cmd

import (
	"crypto/sha256"
	"fmt"
	"testing"
)

// Output hashes and create raw variables captured on fresh main2c468727.
// Initially captured on29c58a0b; the three JSON hashes were re-pinned against
// standalone main after1553 added finishBy:null. Human hashes/inputs unchanged.
// The GraphQL document adds selections; unchanged inputs/output are the contract.
// get json re-pinned for cli#1565: task get --json gains clonedFrom and clones (every
// other hash, and the create variables, are unchanged).
func TestTaskMatching1549LegacyParity(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"create json", []string{"create", "example", "--json"}, "ef123ce9863e1f878f275bdeb26302dcefeb99b116c15ffaa7413cc448859834"},
		{"create human", []string{"create", "example"}, "9f592f1fcb6e727886a55f1d0e9ca674b2d1c76a675e36c89429f2a7b661e6e0"},
		{"get json", []string{"get", "task1", "--json"}, "0566bcb88ca421ade169d828338e018e57a0aa0c272a4fe19c7c9aa7aae40aad"},
		{"get human", []string{"get", "task1"}, "7b142eb9d2927fb33cb0cf711e9200bf4477eceacc34900721f0b9a4993a7d15"},
		{"list json", []string{"list", "--json"}, "4c306c3440669022382ddd4a042886cbb237e8984d7a793d7a3475264d70b6f9"},
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
