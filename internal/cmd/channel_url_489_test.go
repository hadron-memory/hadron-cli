package cmd

import (
 "encoding/json"
 "net/http"
 "net/http/httptest"
 "strings"
 "testing"
)

func TestChannelURL489(t *testing.T) {
 for _, tc := range []struct{name, node string; status int; json bool; wantURL bool}{
 {"json", `{"data":{"node":{"id":"n2","urn":"returned","portalUrl":"https://portal.test/path?q=a%2Fb&x=1"}}}`,200,true,true},
 {"text", `{"data":{"node":{"portalUrl":"https://portal.test/path?q=a%2Fb&x=1"}}}`,200,false,true},
 {"no-origin", `{"data":{"node":{"portalUrl":null}}}`,200,true,false},
 {"concealed", `{"data":{"node":null}}`,200,true,false},
 {"denied", `{"errors":[{"message":"denied","extensions":{"code":"FORBIDDEN"}}]}`,200,true,false},
 {"gateway", `bad gateway`,502,true,false},
 } { t.Run(tc.name,func(t *testing.T){
 var ops []string
 srv:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
 var req struct{ OperationName string; Query string; Variables map[string]any }; _=json.NewDecoder(r.Body).Decode(&req)
 ops=append(ops,req.OperationName)
 w.Header().Set("Content-Type","application/json")
 switch req.OperationName {
 case "GetChannel": if req.Variables["ref"]!="caller-ref" {t.Errorf("caller ref changed")}; _,_=w.Write([]byte(`{"data":{"channel":`+channelNoAddressJSON+`}}`))
 case "NodePortal": if req.Variables["ref"]!="n2" {t.Errorf("root id not used: %v",req.Variables)}; if strings.Contains(req.Query,"content") {t.Error("content selected")}; w.WriteHeader(tc.status); _,_=w.Write([]byte(tc.node))
 default: t.Errorf("unexpected %s",req.OperationName)
 }
 })); defer srv.Close()
 f,out:=testFactory(t); root:=NewRootCmd(f); args:=[]string{"channel","get","caller-ref","--server",srv.URL}; if tc.json {args=append(args,"--json")}; root.SetArgs(args)
 if err:=root.Execute();err!=nil {t.Fatal(err)}
 if strings.Join(ops,",")!="GetChannel,NodePortal" {t.Fatalf("request budget: %v",ops)}
 if tc.json { var got map[string]any; if err:=json.Unmarshal([]byte(out.String()),&got);err!=nil{t.Fatal(err)}; value,ok:=got["portalUrl"]; if !ok {t.Fatal("missing nullable key")}; if tc.wantURL {if value!="https://portal.test/path?q=a%2Fb&x=1"{t.Fatalf("URL changed: %v",value)}} else if value!=nil {t.Fatalf("want null: %v",value)}
 } else if !strings.Contains(out.String(),"URL: https://portal.test/path?q=a%2Fb&x=1") {t.Fatal(out.String())}
 }) }
}
