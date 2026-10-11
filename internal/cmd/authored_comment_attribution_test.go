package cmd

import (
	"context"
	"testing"

	"github.com/Khan/genqlient/graphql"
)

// Fields/arguments come from Diego's authoritative #1606 SDL at 95d1a9bf.
// Operation labels and aliases deliberately differ from generated conventions.
func TestAuthoredCommentFieldsUseTheSharedBindingPolicy(t *testing.T) {
	for _, field := range []string{
		`createComment(targetRef:"n1",body:"body"){id}`,
		`replyToComment(commentRef:"c1",body:"reply"){id}`,
		`editComment(commentRef:"c1",body:"edit",expectedRevision:1){id}`,
		`retractComment(commentRef:"c1",expectedRevision:1){id}`,
		`resolveCommentThread(commentRef:"c1",expectedRevision:1){id}`,
		`reopenCommentThread(commentRef:"c1",expectedRevision:1){id}`,
		`hideComment(commentRef:"c1",reason:"reason",expectedRevision:1){id}`,
		`deleteCommentThread(commentRef:"c1",reason:"reason")`,
	} {
		t.Run(field, func(t *testing.T) {
			writeTeamBinding(t)
			srv, calls := attnServer(t, map[string]string{"Chosen": `{"data":{}}`})
			setTeamBindingServer(t, srv.URL)
			f, _ := testFactory(t)
			_ = NewRootCmd(f)
			f.ServerFlag = srv.URL
			client, err := f.GraphQLClient()
			if err != nil {
				t.Fatal(err)
			}
			req := &graphql.Request{OpName: "Chosen", Query: "mutation Chosen { result:" + field + " }"}
			if err := client.MakeRequest(context.Background(), req, &graphql.Response{}); err != nil {
				t.Fatal(err)
			}
			if len(*calls) != 1 || (*calls)[0].Session != "s-new" {
				t.Fatalf("comment unattributed %+v", *calls)
			}
		})
	}
}

func TestAuthoredPolicyUsesOnlySelectedRootMutationFields(t *testing.T) {
	for _, tc := range []struct {
		name, op, query string
		authored        bool
	}{
		{"renamed variable", "Chosen", `mutation Chosen($p:ID!){created:createComment(targetRef:$p,body:"approveNode"){id}}`, true},
		{"named root fragment", "Chosen", `mutation Chosen {...Calls} fragment Calls on Mutation {createComment(targetRef:"n1",body:"x"){id}}`, true},
		{"inline root fragment", "Chosen", `mutation Chosen {... on Mutation {createComment(targetRef:"n1",body:"x"){id}}}`, true},
		{"selected read", "Reader", `mutation Writer {createComment(targetRef:"n1",body:"x"){id}} query Reader {node(ref:"createComment"){id}}`, false},
		{"selected write", "Writer", `mutation Writer {createComment(targetRef:"n1",body:"x"){id}} query Reader {node(ref:"createComment"){id}}`, true},
		{"operation name is not a field", "CreateComment", `mutation CreateComment {approveNode(nodeRef:"n1"){revision}}`, false},
		{"input text is not a field", "Chosen", `mutation Chosen {updateApp(input:{name:"createComment",data:{createComment:"x"}}){id}}`, false},
		{"mixed maintenance", "Chosen", `mutation Chosen {createComment(targetRef:"n1",body:"x"){id} approveNode(nodeRef:"n1"){revision}}`, false},
		{"unknown selected operation", "Missing", `mutation Chosen {createComment(targetRef:"n1",body:"x"){id}}`, false},
		{"cyclic root fragment", "Chosen", `mutation Chosen {...Calls} fragment Calls on Mutation {...Calls}`, false},
		{"missing root fragment", "Chosen", `mutation Chosen {...Missing}`, false},
		{"malformed document", "Chosen", `mutation Chosen {`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			writeTeamBinding(t)
			srv, calls := attnServer(t, map[string]string{tc.op: `{"data":{}}`})
			setTeamBindingServer(t, srv.URL)
			f, _ := testFactory(t)
			_ = NewRootCmd(f)
			f.ServerFlag = srv.URL
			client, err := f.GraphQLClient()
			if err != nil {
				t.Fatal(err)
			}
			if err := client.MakeRequest(context.Background(), &graphql.Request{OpName: tc.op, Query: tc.query}, &graphql.Response{}); err != nil {
				t.Fatal(err)
			}
			want := ""
			if tc.authored {
				want = "s-new"
			}
			if len(*calls) != 1 || (*calls)[0].Session != want {
				t.Fatalf("session mismatch %+v want=%q", *calls, want)
			}
		})
	}
}
