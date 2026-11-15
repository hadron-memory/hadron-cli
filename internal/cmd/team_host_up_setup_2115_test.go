package cmd

import (
	"testing"
)

func TestHostUpHostSetupFallbackNamesOnlyMissingRoot2115(t *testing.T) {
	for _, tc := range []struct {
		name, raw string
		fallback  bool
	}{
		{"missing root", `{"errors":[{"message":"Cannot query field \"hostSetup\" on type \"Query\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`, true},
		{"nested field", `{"errors":[{"message":"Cannot query field \"futureField\" on type \"HostSetup\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`, false},
		{"unrelated", `{"errors":[{"message":"Unknown argument \"future\" on field \"Query.hostSetup\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`, false},
		{"mixed", `{"errors":[{"message":"Cannot query field \"hostSetup\" on type \"Query\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}},{"message":"forbidden","extensions":{"code":"FORBIDDEN"}}]}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fx := newHostUpFixture1389(t)
			fx.present("Jonas", 0)
			fx.serverState("APPLY", serverWorker1389("wkr-jonas", "Jonas", "RUNNING", "HOLDS_TASK", "", "hadron-cli/jonas"))
			fx.setupRaw = tc.raw
			dto, raw, err := fx.run("--apply")
			if tc.fallback {
				if err != nil {
					t.Fatalf("old server refused: %v %s", err, raw)
				}
				wantItems1389(t, "applied", dto.Applied, "Jonas worker.start NO_SESSION")
				return
			}
			if err == nil {
				t.Fatalf("unrelated schema failure demoted to HOST: %s", raw)
			}
			if len(fx.mutations()) != 0 || len(fx.issued) != 0 {
				t.Fatalf("unread setup issued operations: %v", fx.mutations())
			}
		})
	}
}

func TestHostUpRejectsDuplicateSameHostAssignments2115(t *testing.T) {
	fx := newHostUpFixture1389(t)
	fx.present("Jonas", 0)
	fx.serverState("APPLY", serverWorker1389("wkr-jonas", "Jonas", "RUNNING", "HOLDS_TASK", "", "hadron-cli/jonas"))
	a := assignment2085("hadron-cli/jonas", "claude")
	fx.setup2085("SERVER", a, a)
	_, raw, err := fx.run("--apply")
	if err == nil {
		t.Fatalf("duplicate assignment silently won: %s", raw)
	}
	if len(fx.mutations()) != 0 || len(fx.issued) != 0 {
		t.Fatalf("duplicate setup issued operations: %v", fx.mutations())
	}
}
