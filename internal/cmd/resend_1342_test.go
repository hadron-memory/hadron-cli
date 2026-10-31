package cmd

import (
 "encoding/json"
 "strings"
 "testing"
)

const sender1342 = `{"id":"sender1","organizationId":"org1","from":"Robot <robot@example.test>","enabled":true,"hasApiKey":true,"createdAt":"2026-10-31T00:00:00Z","updatedAt":"2026-10-31T00:00:00Z","createdBy":null,"updatedBy":null}`

func TestResend1342Commands(t *testing.T) {
 for _, tc := range []struct{ name, op, field string; args []string; vars map[string]any; response string }{
  {"create", "CreateResendSender", "createResendSender", []string{"create","--org","acme.com","--from","Robot <robot@example.test>","--api-key-file","-","--enabled=false"}, map[string]any{"orgRef":"acme.com","from":"Robot <robot@example.test>","apiKey":"synthetic-key-1342","enabled":false}, sender1342},
  {"get", "GetResendSender", "resendSender", []string{"get","sender1"}, map[string]any{"ref":"sender1"}, sender1342},
  {"update", "UpdateResendSender", "updateResendSender", []string{"update","sender1","--enabled=false"}, map[string]any{"ref":"sender1","enabled":false}, sender1342},
  {"rotate", "UpdateResendSender", "updateResendSender", []string{"update","sender1","--api-key-file","-"}, map[string]any{"ref":"sender1","apiKey":"synthetic-key-1342"}, sender1342},
  {"list", "ListResendSenders", "resendSenders", []string{"list","--org-id","org1","--limit","3","--offset","2"}, map[string]any{"orgId":"org1","limit":float64(3),"offset":float64(2)}, `{"items":[`+sender1342+`],"total":1}`},
  {"rm", "DeleteResendSender", "deleteResendSender", []string{"rm","sender1","--yes"}, map[string]any{"ref":"sender1"}, `true`},
 } { t.Run(tc.name,func(t *testing.T){
  srv, captured := captureGraphQL(t,map[string]string{tc.op:`{"data":{"`+tc.field+`":`+tc.response+`}}`})
  f,out:=testFactory(t); f.IOStreams.In=strings.NewReader("synthetic-key-1342\n")
  root:=NewRootCmd(f); root.SetArgs(append(append([]string{"org","resend-sender"},tc.args...),"--json","--server",srv.URL))
  if err:=root.Execute();err!=nil {t.Fatal(err)}
  var got map[string]any; if err:=json.Unmarshal(captured[tc.op],&got);err!=nil {t.Fatal(err)}
  for k,v:=range tc.vars { if got[k]!=v {t.Errorf("%s: got %v want %v",k,got[k],v)} }
  if len(got)!=len(tc.vars){t.Errorf("unexpected variables %v",got)}
  if strings.Contains(out.String(),"synthetic-key-1342") {t.Fatal("key leaked")}
  if !json.Valid([]byte(out.String())) {t.Fatalf("invalid JSON %s",out)}
 }) }
}

func TestPlatformInvite1342(t *testing.T) {
 srv,captured:=captureGraphQL(t,map[string]string{"InviteUser":`{"data":{"inviteUser":{"id":"i1","slug":"private-slug","memberRole":"READER","activationCount":0,"createdAt":"2026-10-31T00:00:00Z"}}}`})
 f,out:=testFactory(t); root:=NewRootCmd(f)
 root.SetArgs([]string{"user","invite","--email","alice@example.test","--github-username","alice","--name","Alice","--expires-in-days","7","--max-activations","0","--json","--server",srv.URL})
 if err:=root.Execute();err!=nil {t.Fatal(err)}
 var got map[string]any; _=json.Unmarshal(captured["InviteUser"],&got)
 if got["email"]!="alice@example.test" || got["githubUsername"]!="alice" || got["name"]!="Alice" || got["expiresInDays"]!=float64(7) || got["maxActivations"]!=float64(0) {t.Fatalf("vars %v",got)}
 if !strings.Contains(out.String(),`"slug":"private-slug"`) {t.Fatal(out.String())}
}

func TestMemberRefs1342(t *testing.T) {
 for _, tc:=range []struct{args []string; op, body string}{
 {[]string{"add","acme.com","--user","alice@example.test","--role","READER"},"AddOrgMember",`{"addOrgMember":{"id":"m1","role":"READER","user":`+orgUserJSON+`}}`},
 {[]string{"set-role","acme.com","--user","alice@example.test","--role","READER"},"UpdateOrgMember",`{"updateOrgMember":{"id":"m1","role":"READER","user":`+orgUserJSON+`}}`},
 {[]string{"rm","acme.com","--user","alice@example.test","--yes"},"RemoveOrgMember",`{"removeOrgMember":true}`},
 } { t.Run(tc.op,func(t *testing.T){
 srv,captured:=captureGraphQL(t,map[string]string{tc.op:`{"data":`+tc.body+`}`}); f,_:=testFactory(t); root:=NewRootCmd(f)
 root.SetArgs(append(append([]string{"org","member"},tc.args...),"--server",srv.URL))
 if err:=root.Execute();err!=nil {t.Fatal(err)}
 var got map[string]any; _=json.Unmarshal(captured[tc.op],&got)
 if got["orgRef"]!="acme.com" || got["userRef"]!="alice@example.test" {t.Fatalf("refs not forwarded: %v",got)}
 if _,ok:=got["orgId"];ok {t.Fatal("deprecated orgId")}
 }) }
}
