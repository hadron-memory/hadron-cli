package cmd

import (
	"strings"
	"sync"
	"testing"
)

// wantValidationExitCode is the exit a validation refusal maps to (api.MapError); #2314.
const wantValidationExitCode = 2

const (
	aiOtherValidation2314 = `{"message":"Unknown argument \"bogus\" on field \"Query.resolveAiServiceConfigs\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}`
	aiURNMsg2314          = `{"message":"Cannot query field \"urn\" on type \"AiServiceConfig\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}`
)

// The four callers of isMissingURNField, each with the operation pair it
// retries between: the urn-bearing document and its urn-less fallback.
var aiURNSurfaces2314 = []struct {
	name     string
	args     []string
	withURN  string
	fallback string
	okReply  string
}{
	{"get", []string{"ai-config", "get", "cfg1"}, "AiServiceConfigWithUrn", "AiServiceConfigWithEndpoint", `{"data":{"aiServiceConfig":` + aiDefaultCfgJSON + `}}`},
	{"list --with-endpoints", []string{"ai-config", "list", "--app", "acme.com:juno-app", "--with-endpoints"}, "ResolveAiServiceConfigsWithEndpointAndUrn", "ResolveAiServiceConfigsWithEndpoint", `{"data":{"resolveAiServiceConfigs":[` + aiDefaultCfgJSON + `]}}`},
	{"list", []string{"ai-config", "list", "--app", "acme.com:juno-app"}, "ResolveAiServiceConfigsWithUrn", "ResolveAiServiceConfigs", aiConfigsJSON},
	{"list --all-owners", []string{"ai-config", "list", "--all-owners"}, "AiServiceConfigsMaskedWithUrn", "AiServiceConfigsMasked", `{"data":{"aiServiceConfigs":{"items":[],"total":0}}}`},
}

func runAIURN2314(t *testing.T, args []string, first, withURN, fallback, okReply string) (calls []string, stderr string, err error) {
	t.Helper()
	var mu sync.Mutex
	gql, _ := captureGraphQLFunc(t, func(op string) string {
		mu.Lock()
		calls = append(calls, op)
		mu.Unlock()
		switch op {
		case withURN:
			return first
		case fallback:
			return okReply
		}
		return `{"data":null}`
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append(append([]string{}, args...), "--server", gql.URL))
	err = root.Execute()
	if err != nil {
		renderError(f, err)
	}
	return calls, f.IOStreams.ErrOut.(*strings.Builder).String(), err
}

// A missing-urn message beside another validation message is not "an older
// server rejecting the urn selection and nothing else": one request, and the
// whole refusal reported under its mapped exit code, not retried away.
func TestAiConfigMixedValidationIsNotRetried2314(t *testing.T) {
	mixed := `{"errors":[` + aiURNMsg2314 + `,` + aiOtherValidation2314 + `]}`
	reversed := `{"errors":[` + aiOtherValidation2314 + `,` + aiURNMsg2314 + `]}`
	// The type name, not the rejected field, is "urn" (BLIND P2): a substring
	// match on "urn" would take this for a second missing-urn message.
	collision := `{"errors":[` + aiURNMsg2314 + `,{"message":"Cannot query field \"bogus\" on type \"urn\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`
	collisionOnly := `{"errors":[{"message":"Cannot query field \"bogus\" on type \"urn\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`
	// A blank-message error is still an error: ServerMessages drops it, so a
	// message-only check would take this envelope for a lone missing urn.
	blank := `{"errors":[` + aiURNMsg2314 + `,{"message":"","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`
	other := `{"errors":[` + aiURNMsg2314 + `,{"message":"Cannot query field \"bogus\" on type \"AiServiceConfig\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`
	for _, s := range aiURNSurfaces2314 {
		for name, first := range map[string]string{"mixed": mixed, "reversed": reversed, "other-field": other, "type-name-collision": collision, "collision-alone": collisionOnly, "blank-error": blank} {
			calls, stderr, err := runAIURN2314(t, s.args, first, s.withURN, s.fallback, s.okReply)
			if err == nil || len(calls) != 1 || calls[0] != s.withURN {
				t.Fatalf("%s/%s: want one %s request and an error, got calls %v err %v", s.name, name, s.withURN, calls, err)
			}
			// The refusal keeps its ordinary mapped diagnostic and exit code (the
			// same as any validation refusal), not a success from the retry.
			if got := exitCodeFor(err); got != wantValidationExitCode || !strings.Contains(stderr, "out of sync") {
				t.Fatalf("%s/%s: want exit %d with the mapped diagnostic, got %d %q", s.name, name, wantValidationExitCode, got, stderr)
			}
		}
	}
}

// Control: a single missing-urn message, or the same one repeated, is still the
// older-server shape and retries once without urn.
func TestAiConfigSingleMissingURNStillRetries2314(t *testing.T) {
	single := `{"errors":[` + aiURNMsg2314 + `]}`
	twice := `{"errors":[` + aiURNMsg2314 + `,` + aiURNMsg2314 + `]}`
	for _, s := range aiURNSurfaces2314 {
		for name, first := range map[string]string{"single": single, "repeated": twice} {
			calls, stderr, err := runAIURN2314(t, s.args, first, s.withURN, s.fallback, s.okReply)
			if err != nil || len(calls) != 2 || calls[0] != s.withURN || calls[1] != s.fallback {
				t.Fatalf("%s/%s: want %s then %s, got %v err %v %q", s.name, name, s.withURN, s.fallback, calls, err, stderr)
			}
		}
	}
}

// Control: a failure carrying no server message at all (not a GraphQL
// refusal) is not the older-server shape and is not retried.
func TestAiConfigNoMessageFailureIsNotRetried2314(t *testing.T) {
	for _, s := range aiURNSurfaces2314 {
		calls, _, err := runAIURN2314(t, s.args, `not json`, s.withURN, s.fallback, s.okReply)
		if err == nil || len(calls) != 1 {
			t.Fatalf("%s: want one request and an error, got %v err %v", s.name, calls, err)
		}
	}
}
