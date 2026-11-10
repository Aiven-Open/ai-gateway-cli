// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package fsutil_test

import (
	"path/filepath"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/aiven-open/ai-gateway-cli/internal/fsutil"
)

// fileAllAccess is FA in SDDL, what the GENERIC_ALL the DACL grants maps to for a file.
const fileAllAccess = 0x1f01ff

func assertProtectedOwnerOnlyDACL(t *testing.T, path string, sid *windows.SID) {
	t.Helper()
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	control, _, err := sd.Control()
	if err != nil || control&windows.SE_DACL_PROTECTED == 0 {
		t.Errorf("%s control = %#x, %v; want SE_DACL_PROTECTED", path, control, err)
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	// Compared by SID rather than as SDDL text, which abbreviates well-known accounts: the built-in Administrator a CI
	// runner runs as is LA.
	var ace *windows.ACCESS_ALLOWED_ACE
	if dacl.AceCount != 1 || windows.GetAce(dacl, 0, &ace) != nil || ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE ||
		ace.Mask != fileAllAccess || !(*windows.SID)(unsafe.Pointer(&ace.SidStart)).Equals(sid) {
		t.Errorf("%s SDDL = %q, want a single FA entry for %s", path, sd.String(), sid)
	}
}

func TestWriteAtomicSetsAndKeepsProtectedOwnerOnlyDACLOnWindows(t *testing.T) {
	dir := t.TempDir()
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	sid := user.User.Sid
	privatePath := filepath.Join(dir, "private")
	if err := fsutil.WriteAtomic(privatePath, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	assertProtectedOwnerOnlyDACL(t, privatePath, sid)

	d := openDir(t, dir)
	if err := d.Backup("private", "private.bak"); err != nil {
		t.Fatal(err)
	}
	assertProtectedOwnerOnlyDACL(t, filepath.Join(dir, "private.bak"), sid)

	_, perm, err := fsutil.ReadRegular(privatePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := fsutil.WriteAtomic(privatePath, []byte("updated"), perm); err != nil {
		t.Fatal(err)
	}
	assertProtectedOwnerOnlyDACL(t, privatePath, sid)

	sharedPath := filepath.Join(dir, "shared")
	if err := fsutil.WriteAtomic(sharedPath, []byte("public"), 0o644); err != nil {
		t.Fatal(err)
	}
	sd, err := windows.GetNamedSecurityInfo(sharedPath, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	if control, _, err := sd.Control(); err != nil || control&windows.SE_DACL_PROTECTED != 0 {
		t.Errorf("0644 control = %#x, %v; want inherited DACL", control, err)
	}

	deniedSID, err := windows.CreateWellKnownSid(windows.WinAnonymousSid)
	if err != nil {
		t.Fatal(err)
	}
	existingDACL, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	dacl, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{{
		AccessPermissions: windows.GENERIC_READ,
		AccessMode:        windows.DENY_ACCESS,
		Trustee: windows.TRUSTEE{
			TrusteeForm:  windows.TRUSTEE_IS_SID,
			TrusteeType:  windows.TRUSTEE_IS_WELL_KNOWN_GROUP,
			TrusteeValue: windows.TrusteeValueFromSID(deniedSID),
		},
	}}, existingDACL)
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetNamedSecurityInfo(
		sharedPath,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.UNPROTECTED_DACL_SECURITY_INFORMATION,
		nil, nil, dacl, nil,
	); err != nil {
		t.Fatal(err)
	}
	assertUnprotectedDACLWithDeny(t, sharedPath, deniedSID)

	if err := d.Backup("shared", "shared.bak"); err != nil {
		t.Fatal(err)
	}
	assertUnprotectedDACLWithDeny(t, filepath.Join(dir, "shared.bak"), deniedSID)

	if err := fsutil.WriteAtomic(sharedPath, []byte("updated"), 0o644); err != nil {
		t.Fatal(err)
	}
	assertUnprotectedDACLWithDeny(t, sharedPath, deniedSID)
}

func assertUnprotectedDACLWithDeny(t *testing.T, path string, deniedSID *windows.SID) {
	t.Helper()
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	if control, _, err := sd.Control(); err != nil || control&windows.SE_DACL_PROTECTED != 0 {
		t.Errorf("%s control = %#x, %v; want unprotected DACL", path, control, err)
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	for i := uint32(0); i < uint32(dacl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if windows.GetAce(dacl, i, &ace) == nil && ace.Header.AceType == windows.ACCESS_DENIED_ACE_TYPE &&
			(*windows.SID)(unsafe.Pointer(&ace.SidStart)).Equals(deniedSID) {
			return
		}
	}
	t.Errorf("%s SDDL = %q, want explicit deny ACE for %s", path, sd.String(), deniedSID)
}
