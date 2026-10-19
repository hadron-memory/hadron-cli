package cmd

import (
 "encoding/json"
 "strings"
 "testing"
)

const tickerFixture = `{"id":"t1","urn":null,"name":"daily","slug":"daily","app":{"id":"app1"},"cadenceSeconds":600,"anchorAt":"2026-10-19T00:00:00Z","quietHours":null,"channels":[],"relevance":"ALL","reasons":["READ_TEAM_CHANNEL","EVERY_TICK"],"enabled":true,"owner":null,"nextTickAt":null,"lastTickAt":null,"createdAt":"2026-10-19T00:00:00Z","updatedAt":null}`

func TestTickerCreateReasonsWire(t *testing.T) {
 for _, reasons := range []string{"", "EVERY_TICK,read-team-channel,every-tick"} {
  t.Run(reasons,func(t *testing.T){
   gql, captured := captureGraphQL(t,map[string]string{"CreateTicker":`{"data":{"createTicker":`+tickerFixture+`}}`})
   f,out:=testFactory(t); root:=NewRootCmd(f)
   args:=[]string{"ticker","create","--app","acme.com:ops","--name","daily","--cadence-seconds","600","--json","--server",gql.URL}
   if reasons!="" {args=append(args,"--reasons",reasons)}
   root.SetArgs(args); if err:=root.Execute();err!=nil {t.Fatal(err)}
   var v struct{Input map[string]any}; if err:=json.Unmarshal(captured["CreateTicker"],&v);err!=nil{t.Fatal(err)}
   for _,key:=range []string{"enabled","anchorAt","quietHours","channelIds","relevance"} {if _,ok:=v.Input[key];ok{t.Errorf("omitted %s sent: %s",key,captured["CreateTicker"])}}
   if reasons=="" {if _,ok:=v.Input["reasons"];ok{t.Fatal("default must be server-owned")}} else if got,_:=json.Marshal(v.Input["reasons"]);string(got)!=`["READ_TEAM_CHANNEL","EVERY_TICK"]`{t.Fatalf("reasons %s",got)}
   if !strings.Contains(out.String(),`"urn": null`) || !strings.Contains(out.String(),`"every-tick"`){t.Fatal(out.String())}
  })
 }
}

func TestTickerRejectsInvalidInputsBeforeNetwork(t *testing.T){
 for _,args:=range [][]string{
  {"create","--name","n","--cadence-seconds","600","--reasons="},
  {"create","--name","n","--cadence-seconds","600","--reasons","every-tick,"},
  {"create","--name","n","--cadence-seconds","600","--reasons","unknown"},
  {"update","t1","--quiet-hours","{}","--clear-quiet-hours"},
  {"update","t1","--channel-ids","c1","--clear-channels"},
  {"get","a-bare-name"},
  {"delete","t1"},
 } {t.Run(strings.Join(args," "),func(t *testing.T){f,_:=testFactory(t);r:=NewRootCmd(f);r.SetArgs(append([]string{"ticker"},args...));if err:=r.Execute();err==nil{t.Fatal("expected refusal")}})}
}

func TestTickerUpdateOmitAndFalse(t *testing.T){
 gql,captured:=captureGraphQL(t,map[string]string{"UpdateTicker":`{"data":{"updateTicker":`+tickerFixture+`}}`})
 f,_:=testFactory(t);r:=NewRootCmd(f);r.SetArgs([]string{"ticker","update","t1","--enabled=false","--clear-channels","--clear-quiet-hours","--server",gql.URL});if err:=r.Execute();err!=nil{t.Fatal(err)}
 var v struct{Input map[string]any};_ = json.Unmarshal(captured["UpdateTicker"],&v)
 if len(v.Input)!=3||v.Input["enabled"]!=false||v.Input["clearChannels"]!=true||v.Input["clearQuietHours"]!=true{t.Fatalf("wire %s",captured["UpdateTicker"])}
}

func TestTickerGetNull(t *testing.T){
 gql:=fakeGraphQL(t,map[string]string{"GetTicker":`{"data":{"ticker":null}}`});f,_:=testFactory(t);r:=NewRootCmd(f);r.SetArgs([]string{"ticker","get","t1","--server",gql.URL});if err:=r.Execute();err==nil||!strings.Contains(err.Error(),"not found"){t.Fatalf("%v",err)}
}
