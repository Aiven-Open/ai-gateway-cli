// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package fsutil

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// readFlags need nothing more on Windows, which has no FIFOs in the file system; the check after the open catches a
// symlink swapped in.
const readFlags = os.O_RDONLY

var reOpenFile = windows.NewLazySystemDLL("kernel32.dll").NewProc("ReOpenFile")

// setMode sets mode on f. For a 0600 file it replaces f's Windows DACL with a protected one granting access only to
// the current user, and when replacing an existing file with another mode it keeps the DACL and protection state from
// the original file, since Chmod and Stat on Windows carry only the read-only attribute.
func (d *Dir) setMode(name string, f *os.File, mode os.FileMode) error {
	if err := f.Chmod(mode); err != nil {
		return err
	}
	if mode.Perm() != 0o600 {
		// A file that is not there yet has no DACL to keep; anything else openRegular refuses stops the write.
		src, _, err := d.openRegular(name)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		defer func() { _ = src.Close() }()
		return copyDACL(src, f)
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return err
	}
	dacl, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{{
		AccessPermissions: windows.GENERIC_ALL,
		AccessMode:        windows.GRANT_ACCESS,
		Trustee: windows.TRUSTEE{
			TrusteeForm:  windows.TRUSTEE_IS_SID,
			TrusteeType:  windows.TRUSTEE_IS_USER,
			TrusteeValue: windows.TrusteeValueFromSID(user.User.Sid),
		},
	}}, nil)
	if err != nil {
		return err
	}
	return setDACL(f, dacl, windows.PROTECTED_DACL_SECURITY_INFORMATION)
}

func copyMode(src, dst *os.File, mode os.FileMode) error {
	if err := dst.Chmod(mode); err != nil {
		return err
	}
	return copyDACL(src, dst)
}

func copyDACL(src, dst *os.File) error {
	sd, err := windows.GetSecurityInfo(
		windows.Handle(src.Fd()),
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION,
	)
	if err != nil {
		return err
	}
	control, _, err := sd.Control()
	if err != nil {
		return err
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	protection := windows.SECURITY_INFORMATION(windows.UNPROTECTED_DACL_SECURITY_INFORMATION)
	if control&windows.SE_DACL_PROTECTED != 0 {
		protection = windows.PROTECTED_DACL_SECURITY_INFORMATION
	}
	return setDACL(dst, dacl, protection)
}

func setDACL(f *os.File, dacl *windows.ACL, protection windows.SECURITY_INFORMATION) error {
	// Setting a DACL needs WRITE_DAC on the handle, which os opens a file without. ReOpenFile asks for it on the same
	// file object, so no path is looked up again.
	r, _, errno := reOpenFile.Call(f.Fd(), windows.READ_CONTROL|windows.WRITE_DAC,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, 0)
	h := windows.Handle(r)
	if h == windows.InvalidHandle {
		return errno
	}
	defer func() { _ = windows.CloseHandle(h) }()
	return windows.SetSecurityInfo(
		h,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|protection,
		nil, nil, dacl, nil,
	)
}
