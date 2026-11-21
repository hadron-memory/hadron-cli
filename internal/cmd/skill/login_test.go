package skill

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/Khan/genqlient/graphql"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
)

func TestLoginAvailabilityDoesNotInventAnEmptyAnswer(t *testing.T) {
	for _, c := range []struct {
		name, body       string
		enabled, wantErr bool
	}{
		{"empty", `{"data":{"skillPlan":{"entries":[]}}}`, false, false},
		{"disabled", `{"data":{"skillPlan":{"entries":[{"class":"disabled"}]}}}`, false, false},
		{"current", `{"data":{"skillPlan":{"entries":[{"class":"current"}]}}}`, true, false},
		{"mixed", `{"data":{"skillPlan":{"entries":[{"class":"disabled"},{"class":"never-exported"}]}}}`, true, false},
		{"null-entries", `{"data":{"skillPlan":{"entries":null}}}`, false, true},
		{"null-plan", `{"data":{"skillPlan":null}}`, false, true},
		{"null-entry", `{"data":{"skillPlan":{"entries":[null]}}}`, false, true},
		{"unknown-class", `{"data":{"skillPlan":{"entries":[{"class":null}]}}}`, false, true},
		{"refused", `{"errors":[{"message":"synthetic refused","extensions":{"code":"FORBIDDEN"}}]}`, false, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, c.body) }))
			defer srv.Close()
			enabled, err := HasLoginSkills(context.Background(), graphql.NewClient(srv.URL, srv.Client()), []string{"codexSkill"})
			if enabled != c.enabled || (err != nil) != c.wantErr {
				t.Fatalf("enabled=%v err=%v", enabled, err)
			}
		})
	}
}

func TestLoginAvailabilityDoesNotFetchRenderedSkills(t *testing.T) {
	// The pre-consent query is a small projection of the existing server planner,
	// not a fetch of the team's full rendered skill corpus before a person agrees.
	// The unused fields are matched as exact word-boundary tokens anywhere in the
	// operation, so the pin holds whatever line format genqlient emits (#2500:
	// independent of generated-code layout — a field squeezed onto a shared line
	// is still caught).
	const prefix = "query LoginSkillAvailability"
	if !strings.Contains(gen.LoginSkillAvailability_Operation, prefix) {
		t.Fatal("wrong availability operation")
	}
	unused := regexp.MustCompile(`\b(renderedBody|findings|orphans|nodeId|name)\b`)
	if got := unused.FindString(gen.LoginSkillAvailability_Operation); got != "" {
		t.Errorf("preflight selects unused %s", got)
	}
}
