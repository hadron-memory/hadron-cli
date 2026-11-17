package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Khan/genqlient/graphql"
)

// cli#1624: the ledger is diagnostics only. Nothing sent, or a mix of kinds,
// is unknown provenance and must not read as "only the stored login".
func TestCredentialLedgerOnly(t *testing.T) {
	var nilLedger *CredentialLedger
	nilLedger.Sent(CredentialStoredUser) // a nil ledger records nothing, and must not panic
	if nilLedger.Only(CredentialStoredUser) {
		t.Error("a nil ledger reports nothing")
	}
	l := &CredentialLedger{}
	if l.Only(CredentialStoredUser) {
		t.Error("nothing sent must not read as the stored login")
	}
	l.Sent("")
	if l.Only(CredentialStoredUser) {
		t.Error("an empty kind is not recorded")
	}
	l.Sent(CredentialStoredUser)
	l.Sent(CredentialStoredUser)
	if !l.Only(CredentialStoredUser) {
		t.Error("only the stored login was sent")
	}
	l.Sent(CredentialHostKey)
	if l.Only(CredentialStoredUser) || l.Only(CredentialHostKey) {
		t.Error("a mix of credentials is unknown provenance")
	}
}

// The typed client records its kind when a request carrying its token goes
// out, and a tokenless client records nothing. Recording never changes the
// header that is sent.
func TestWithCredentialSentRecordsOnlyWhatWasSent(t *testing.T) {
	var gotAuth []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = append(gotAuth, r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{}}`))
	}))
	defer srv.Close()

	call := func(c graphql.Client) {
		var resp graphql.Response
		_ = c.MakeRequest(context.Background(), &graphql.Request{OpName: "Probe", Query: "query Probe { __typename }"}, &resp)
	}

	anon := &CredentialLedger{}
	c, err := NewClient(srv.URL, "", srv.Client(), WithCredentialSent(anon, CredentialStoredUser))
	if err != nil {
		t.Fatal(err)
	}
	call(c)
	if anon.Only(CredentialStoredUser) {
		t.Error("a request without a token must not record a credential")
	}

	l := &CredentialLedger{}
	if l.Only(CredentialHostKey) {
		t.Fatal("nothing recorded before a request")
	}
	c, err = NewClient(srv.URL, "hk_key", srv.Client(), WithCredentialSent(l, CredentialHostKey))
	if err != nil {
		t.Fatal(err)
	}
	if l.Only(CredentialHostKey) {
		t.Error("building a client is not sending a request")
	}
	call(c)
	if !l.Only(CredentialHostKey) {
		t.Error("the sent host key was not recorded")
	}
	if len(gotAuth) != 2 || gotAuth[0] != "" || gotAuth[1] != "Bearer hk_key" {
		t.Errorf("recording must not change what is sent: %q", gotAuth)
	}
}
