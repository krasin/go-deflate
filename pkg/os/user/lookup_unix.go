// Copyright 2011 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// +build darwin freebsd linux
// +build cgo

package user

import (
	"fmt"
	"runtime"
	"strings"
	"syscall"
	"unsafe"
)

/*
#include <unistd.h>
#include <sys/types.h>
#include <pwd.h>
#include <stdlib.h>

static int mygetpwuid_r(int uid, struct passwd *pwd,
	char *buf, size_t buflen, struct passwd **result) {
 return getpwuid_r(uid, pwd, buf, buflen, result);
}
*/
import "C"

func init() {
	implemented = true
}

// Lookup looks up a user by username. If the user cannot be found,
// the returned error is of type UnknownUserError.
func Lookup(username string) (*User, error) {
	return lookup(-1, username, true)
}

// LookupId looks up a user by userid. If the user cannot be found,
// the returned error is of type UnknownUserIdError.
func LookupId(uid int) (*User, error) {
	return lookup(uid, "", false)
}

func lookup(uid int, username string, lookupByName bool) (*User, error) {
	var pwd C.struct_passwd
	var result *C.struct_passwd

	var bufSize C.long
	if runtime.GOOS == "freebsd" {
		// FreeBSD doesn't have _SC_GETPW_R_SIZE_MAX
		// and just returns -1.  So just use the same
		// size that Linux returns
		bufSize = 1024
	} else {
		bufSize = C.sysconf(C._SC_GETPW_R_SIZE_MAX)
		if bufSize <= 0 || bufSize > 1<<20 {
			return nil, fmt.Errorf("user: unreasonable _SC_GETPW_R_SIZE_MAX of %d", bufSize)
		}
	}
	buf := C.malloc(C.size_t(bufSize))
	defer C.free(buf)
	var rv C.int
	if lookupByName {
		nameC := C.CString(username)
		defer C.free(unsafe.Pointer(nameC))
		rv = C.getpwnam_r(nameC,
			&pwd,
			(*C.char)(buf),
			C.size_t(bufSize),
			&result)
		if rv != 0 {
			return nil, fmt.Errorf("user: lookup username %s: %s", username, syscall.Errno(rv))
		}
		if result == nil {
			return nil, UnknownUserError(username)
		}
	} else {
		// mygetpwuid_r is a wrapper around getpwuid_r to
		// to avoid using uid_t because C.uid_t(uid) for
		// unknown reasons doesn't work on linux.
		rv = C.mygetpwuid_r(C.int(uid),
			&pwd,
			(*C.char)(buf),
			C.size_t(bufSize),
			&result)
		if rv != 0 {
			return nil, fmt.Errorf("user: lookup userid %d: %s", uid, syscall.Errno(rv))
		}
		if result == nil {
			return nil, UnknownUserIdError(uid)
		}
	}
	u := &User{
		Uid:      int(pwd.pw_uid),
		Gid:      int(pwd.pw_gid),
		Username: C.GoString(pwd.pw_name),
		Name:     C.GoString(pwd.pw_gecos),
		HomeDir:  C.GoString(pwd.pw_dir),
	}
	// The pw_gecos field isn't quite standardized.  Some docs
	// say: "It is expected to be a comma separated list of
	// personal data where the first item is the full name of the
	// user."
	if i := strings.Index(u.Name, ","); i >= 0 {
		u.Name = u.Name[:i]
	}
	return u, nil
}
