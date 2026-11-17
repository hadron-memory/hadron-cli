package secretfile

// ownerOnlySDDL is the security descriptor a key file is created with on
// windows (#1325): a protected DACL ("P": the directory's ACEs are not
// inherited) with one ALLOW ACE of FILE_ALL_ACCESS ("FA") for the current user
// and one for SYSTEM ("SY"), so backup and AV tooling keep working. No
// Administrators, Users or Everyone entry (Holger, ruling 0244).
//
// It lives outside the windows files so its text is unit-tested on every
// platform.
func ownerOnlySDDL(userSID string) string {
	return "D:P(A;;FA;;;" + userSID + ")(A;;FA;;;SY)"
}
