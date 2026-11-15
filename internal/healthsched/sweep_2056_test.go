package healthsched

import (
 "strings"
 "testing"
 "time"
)

func TestSweepUnitHasCrashLimitAndOneMinuteTimer2056(t *testing.T) {
 s := Spec{Job:Job{Name:"hadron-team-sweep",Sub:[]string{"team","host","sweep"},Describes:"team sweep"},Bin:"/usr/bin/hadron",Config:"/etc/hadron/host.yaml",Interval:time.Minute}
 unit:=SystemdService(s)
 for _,want:=range []string{"StartLimitBurst=3","StartLimitIntervalSec=300","OnFailure=","\"team\" \"host\" \"sweep\""} {
  if !strings.Contains(unit,want) { t.Errorf("unit lacks %s: %s",want,unit) }
 }
 if !strings.Contains(SystemdTimer(s),"OnUnitActiveSec=1min") {t.Fatal("sweep timer must be one minute")}
}
