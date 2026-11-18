package auth

import (
	"os"
	"os/exec"
)

// Environment reads are replaceable so command tests never inspect or write
// the developer's installed skills. No process environment is changed.
var loginSkillsHome = os.UserHomeDir
var loginSkillsLookPath = exec.LookPath
