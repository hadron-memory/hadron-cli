package cmd

import (
 "encoding/json"
 "reflect"
 "testing"
)

func Test2138TickerLiveCheckReasonsWire(t *testing.T) {
 for _,verb:=range []string{"create","update"} {
  for _,reason:=range []string{"live-check","LIVE_CHECK,read-team-channel,live-check"} {
   t.Run(verb+"/"+reason,func(t *testing.T){
    op,field:="CreateTicker","createTicker"
    args:=[]string{"ticker","create","--app","acme.com:ops","--name","checks","--cadence-seconds","600"}
    if verb=="update" {op,field="UpdateTicker","updateTicker";args=[]string{"ticker","update",tickerRef}}
    gql,captured:=captureGraphQL(t,map[string]string{op:`{"data":{"`+field+`":`+tickerFixture+`}}`})
    f,_:=testFactory(t);root:=NewRootCmd(f);root.SetArgs(append(args,"--reasons",reason,"--server",gql.URL))
    if err:=root.Execute();err!=nil{t.Fatal(err)}
    var vars struct{Input struct{Reasons []string `json:"reasons"`} `json:"input"`};if err:=json.Unmarshal(captured[op],&vars);err!=nil{t.Fatal(err)}
    want:=[]string{"LIVE_CHECK"};if reason!="live-check" {want=[]string{"READ_TEAM_CHANNEL","LIVE_CHECK"}}
    if !reflect.DeepEqual(vars.Input.Reasons,want){t.Fatalf("wire reasons %v want %v",vars.Input.Reasons,want)}
   })
  }
 }
}
