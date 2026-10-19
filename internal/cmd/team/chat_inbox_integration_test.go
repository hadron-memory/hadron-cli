package team

import (
 "testing"
 "github.com/hadron-memory/hadron-cli/internal/cmdutil"
)

func TestInboxProductionRegistered(t *testing.T) {
 c:=NewCmdTeam(cmdutil.NewFactory())
 found,_,err:=c.Find([]string{"chat","inbox"})
 if err!=nil || found.Name()!="inbox" { t.Fatalf("production inbox missing: %v %s",err,found.Name()) }
}
