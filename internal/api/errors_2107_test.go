package api

import (
	"testing"

	"github.com/Khan/genqlient/graphql"
	"github.com/vektah/gqlparser/v2/gqlerror"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

func TestShortLivedTickerRefusal2107(t *testing.T) {
	refusal := gqlerror.List{{Message: "provider wording", Extensions: map[string]any{"code": "TICKER_ON_SHORT_LIVED"}}}
	for _, raw := range []error{refusal, &graphql.HTTPError{StatusCode: 500, Response: graphql.Response{Errors: refusal}}} {
		err := MapError(raw)
		if exitcode.FromError(err) != exitcode.Conflict || err.Error() != "a short-lived worker is woken by its tasks, not by tickers" {
			t.Fatalf("mapped %v (exit %d)", err, exitcode.FromError(err))
		}
		if !HasErrorCode(err, "TICKER_ON_SHORT_LIVED") {
			t.Fatal("mapping lost the typed refusal")
		}
	}
}
