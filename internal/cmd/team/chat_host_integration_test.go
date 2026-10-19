package team
import (
 "testing"
 "github.com/hadron-memory/hadron-cli/internal/cmdutil"
)
func TestPostProductionHostFlag(t *testing.T){
 if newCmdTeamChatPost(cmdutil.NewFactory()).Flags().Lookup("orca-host-config")==nil{t.Fatal("production host delivery opt-in missing")}
}
