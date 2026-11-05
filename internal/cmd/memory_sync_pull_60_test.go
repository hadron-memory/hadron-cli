package cmd

import (
 "encoding/json"
 "strings"
 "testing"
)

func TestMemorySyncPull60(t *testing.T) {
 for _, tc := range []struct{name string; report *string; at *string; clean bool; status string}{
  {"clean",nil,sp(syncedAt),true,"none"},
  {"partial",sp(partialReport()),sp(syncedAt),false,"recorded"},
  {"unknown",sp(`{"version":1,"outcome":"unknown","recordedAt":"`+recordedAt+`","entries":[]}`),sp(syncedAt),false,"recorded"},
  {"malformed",sp(`{`),sp(syncedAt),false,"unavailable"},
  {"version",sp(`{"version":2}`),sp(syncedAt),false,"unavailable"},
  {"outcome",sp(`{"version":1,"outcome":"new","recordedAt":"`+recordedAt+`","entries":[]}`),sp(syncedAt),false,"unavailable"},
  {"never",nil,nil,false,"none"},
 } { t.Run(tc.name,func(t *testing.T){
  for _, asJSON := range []bool{false,true} {
   gql,calls:=captureGraphQL(t,map[string]string{"SyncMemory":`{"data":{"syncMemory":`+syncMemoryJSON(t,nil,tc.at,nil,tc.report,"OK")+`}}`})
   f,out:=testFactory(t); root:=NewRootCmd(f)
   args:=[]string{"memory","sync","pull","acme.com:kb","--yes","--server",gql.URL}; if asJSON {args=append(args,"--json")};root.SetArgs(args)
   if err:=root.Execute();err!=nil {t.Fatal(err)}
   var vars map[string]any; if err:=json.Unmarshal(calls["SyncMemory"],&vars);err!=nil{t.Fatal(err)}
   if len(vars)!=1 || vars["id"]!="hrn:mem:acme.com:kb" {t.Fatalf("variables: %v",vars)}
   if asJSON {var dto map[string]any;if err:=json.Unmarshal([]byte(out.String()),&dto);err!=nil{t.Fatal(err)}; if dto["clean"]!=tc.clean || dto["syncReportStatus"]!=tc.status || dto["operation"]!="pull"{t.Fatalf("dto: %v",dto)}
    if tc.name=="partial" {rep:=dto["syncReport"].(map[string]any);if len(rep["entries"].([]any))!=2{t.Fatal(rep)}}
   } else {if tc.name=="partial" {for _,s:=range []string{"skipped or held","a:b","PROTECTED_LOC","new:place","MOVE_HELD"}{if !strings.Contains(out.String(),s){t.Fatalf("missing %s: %s",s,out)}}}; if !tc.clean && strings.Contains(out.String(),"clean sync"){t.Fatal(out)}}
  }
 }) }
}

func TestMemorySyncPull60Confirm(t *testing.T){
 for _,tty:=range []bool{false,true}{
  gql,calls:=captureGraphQL(t,nil); f,out:=testFactory(t); if tty {f, out,_=testFactoryTTY(t,"n\n")}; root:=NewRootCmd(f);root.SetArgs([]string{"memory","sync","pull","acme.com:kb","--server",gql.URL})
  err:=root.Execute();want:=2;if tty {want=6};if exitCodeFor(err)!=want || len(calls)!=0 || strings.Contains(out.String(),"synced"){t.Fatalf("err %v calls %v out %s",err,calls,out)}
 }
}

func TestMemorySyncPull60Refusals(t *testing.T){
 for _,tc:=range []struct{code string; exit int}{{"ENCRYPTED_GIT_SYNC_UNAVAILABLE",2},{"SYNC_FAILED",1},{"BAD_USER_INPUT",2},{"FORBIDDEN",8},{"NOT_FOUND",4},{"",1}}{
  t.Run(tc.code,func(t *testing.T){
   response:=`{"errors":[{"message":"failed https://user:tok@host/repo","extensions":{"code":"`+tc.code+`"}}]}`
   gql,calls:=captureGraphQL(t,map[string]string{"SyncMemory":response});f,out:=testFactory(t);root:=NewRootCmd(f);root.SetArgs([]string{"memory","sync","pull","acme.com:kb","--yes","--server",gql.URL});err:=root.Execute()
   if exitCodeFor(err)!=tc.exit || len(calls)!=1{t.Fatalf("err %v calls %v",err,calls)}
   if strings.Contains(err.Error(),"user:tok") || strings.Contains(out.String(),"user:tok"){t.Fatal("credential leak")}
   if tc.code=="SYNC_FAILED" && !strings.Contains(err.Error(),"[redacted]"){t.Fatal(err)}
   if tc.code=="ENCRYPTED_GIT_SYNC_UNAVAILABLE" {for _,s:=range []string{"unlock","export"}{if strings.Contains(err.Error(),s){t.Fatal(err)}}}
  })
 }
}
