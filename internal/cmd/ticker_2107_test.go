package cmd

import (
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

func TestTickerShortLivedRefusal2107(t *testing.T) {
	for _, jsonOutput := range []bool{false, true} {
		calls := 0
		gql, _ := captureGraphQLFunc(t, func(op string) string {
			if op == "SubscribeTicker" {
				calls++
				return `{"data":null,"errors":[{"message":"provider wording","extensions":{"code":"TICKER_ON_SHORT_LIVED"}}]}`
			}
			return ""
		})
		f, out := testFactory(t)
		root := NewRootCmd(f)
		args := []string{"ticker", "subscribe", tickerRef, "--worker", workerRef, "--server", gql.URL}
		if jsonOutput {
			args = append(args, "--json")
		}
		root.SetArgs(args)
		err := root.Execute()
		if exitcode.FromError(err) != exitcode.Conflict || err == nil || !strings.Contains(err.Error(), "a short-lived worker is woken by its tasks, not by tickers") || calls != 1 || out.Len() != 0 {
			t.Fatalf("json=%v err=%v exit=%d calls=%d out=%s", jsonOutput, err, exitcode.FromError(err), calls, out)
		}
	}
}

func TestTickerShortLivedUnsubscribeStillAllowed2107(t *testing.T) {
	gql, calls := captureGraphQL(t, map[string]string{"UnsubscribeTicker": `{"data":{"unsubscribeWorker":{"subscription":` + subscriptionFixture + `,"announcement":null}}}`})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"ticker", "unsubscribe", tickerRef, "--worker", workerRef, "--server", gql.URL, "--json"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if _, ok := calls["UnsubscribeTicker"]; !ok || len(calls) != 1 || out.Len() == 0 {
		t.Fatalf("calls=%+v out=%s", calls, out)
	}
}
