// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package os_test

import (
	. "os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestMkdirAll(t *testing.T) {
	// Create new dir, in _test so it will get
	// cleaned up by make if not by us.
	path := "_test/_TestMkdirAll_/dir/./dir2"
	err := MkdirAll(path, 0777)
	if err != nil {
		t.Fatalf("MkdirAll %q: %s", path, err)
	}
	defer RemoveAll("_test/_TestMkdirAll_")

	// Already exists, should succeed.
	err = MkdirAll(path, 0777)
	if err != nil {
		t.Fatalf("MkdirAll %q (second time): %s", path, err)
	}

	// Make file.
	fpath := path + "/file"
	f, err := Create(fpath)
	if err != nil {
		t.Fatalf("create %q: %s", fpath, err)
	}
	defer f.Close()

	// Can't make directory named after file.
	err = MkdirAll(fpath, 0777)
	if err == nil {
		t.Fatalf("MkdirAll %q: no error", fpath)
	}
	perr, ok := err.(*PathError)
	if !ok {
		t.Fatalf("MkdirAll %q returned %T, not *PathError", fpath, err)
	}
	if filepath.Clean(perr.Path) != filepath.Clean(fpath) {
		t.Fatalf("MkdirAll %q returned wrong error path: %q not %q", fpath, filepath.Clean(perr.Path), filepath.Clean(fpath))
	}

	// Can't make subdirectory of file.
	ffpath := fpath + "/subdir"
	err = MkdirAll(ffpath, 0777)
	if err == nil {
		t.Fatalf("MkdirAll %q: no error", ffpath)
	}
	perr, ok = err.(*PathError)
	if !ok {
		t.Fatalf("MkdirAll %q returned %T, not *PathError", ffpath, err)
	}
	if filepath.Clean(perr.Path) != filepath.Clean(fpath) {
		t.Fatalf("MkdirAll %q returned wrong error path: %q not %q", ffpath, filepath.Clean(perr.Path), filepath.Clean(fpath))
	}

	if runtime.GOOS == "windows" {
		path := `_test\_TestMkdirAll_\dir\.\dir2\`
		err := MkdirAll(path, 0777)
		if err != nil {
			t.Fatalf("MkdirAll %q: %s", path, err)
		}
	}
}

func TestRemoveAll(t *testing.T) {
	// Work directory.
	path := "_test/_TestRemoveAll_"
	fpath := path + "/file"
	dpath := path + "/dir"

	// Make directory with 1 file and remove.
	if err := MkdirAll(path, 0777); err != nil {
		t.Fatalf("MkdirAll %q: %s", path, err)
	}
	fd, err := Create(fpath)
	if err != nil {
		t.Fatalf("create %q: %s", fpath, err)
	}
	fd.Close()
	if err = RemoveAll(path); err != nil {
		t.Fatalf("RemoveAll %q (first): %s", path, err)
	}
	if _, err := Lstat(path); err == nil {
		t.Fatalf("Lstat %q succeeded after RemoveAll (first)", path)
	}

	// Make directory with file and subdirectory and remove.
	if err = MkdirAll(dpath, 0777); err != nil {
		t.Fatalf("MkdirAll %q: %s", dpath, err)
	}
	fd, err = Create(fpath)
	if err != nil {
		t.Fatalf("create %q: %s", fpath, err)
	}
	fd.Close()
	fd, err = Create(dpath + "/file")
	if err != nil {
		t.Fatalf("create %q: %s", fpath, err)
	}
	fd.Close()
	if err = RemoveAll(path); err != nil {
		t.Fatalf("RemoveAll %q (second): %s", path, err)
	}
	if _, err := Lstat(path); err == nil {
		t.Fatalf("Lstat %q succeeded after RemoveAll (second)", path)
	}

	// Determine if we should run the following test.
	testit := true
	if runtime.GOOS == "windows" {
		// Chmod is not supported under windows.
		testit = false
	} else {
		// Test fails as root.
		testit = Getuid() != 0
	}
	if testit {
		// Make directory with file and subdirectory and trigger error.
		if err = MkdirAll(dpath, 0777); err != nil {
			t.Fatalf("MkdirAll %q: %s", dpath, err)
		}

		for _, s := range []string{fpath, dpath + "/file1", path + "/zzz"} {
			fd, err = Create(s)
			if err != nil {
				t.Fatalf("create %q: %s", s, err)
			}
			fd.Close()
		}
		if err = Chmod(dpath, 0); err != nil {
			t.Fatalf("Chmod %q 0: %s", dpath, err)
		}

		// No error checking here: either RemoveAll
		// will or won't be able to remove dpath;
		// either way we want to see if it removes fpath
		// and path/zzz.  Reasons why RemoveAll might
		// succeed in removing dpath as well include:
		//	* running as root
		//	* running on a file system without permissions (FAT)
		RemoveAll(path)
		Chmod(dpath, 0777)

		for _, s := range []string{fpath, path + "/zzz"} {
			if _, err := Lstat(s); err == nil {
				t.Fatalf("Lstat %q succeeded after partial RemoveAll", s)
			}
		}
	}
	if err = RemoveAll(path); err != nil {
		t.Fatalf("RemoveAll %q after partial RemoveAll: %s", path, err)
	}
	if _, err := Lstat(path); err == nil {
		t.Fatalf("Lstat %q succeeded after RemoveAll (final)", path)
	}
}

func TestMkdirAllWithSymlink(t *testing.T) {
	if runtime.GOOS == "windows" || runtime.GOOS == "plan9" {
		t.Log("Skipping test: symlinks don't exist under Windows/Plan 9")
		return
	}

	err := Mkdir("_test/dir", 0755)
	if err != nil {
		t.Fatal(`Mkdir "_test/dir":`, err)
	}
	defer RemoveAll("_test/dir")

	err = Symlink("dir", "_test/link")
	if err != nil {
		t.Fatal(`Symlink "dir", "_test/link":`, err)
	}
	defer RemoveAll("_test/link")

	path := "_test/link/foo"
	err = MkdirAll(path, 0755)
	if err != nil {
		t.Errorf("MkdirAll %q: %s", path, err)
	}
}

func TestMkdirAllAtSlash(t *testing.T) {
	if runtime.GOOS == "windows" || runtime.GOOS == "plan9" {
		return
	}
	RemoveAll("/_go_os_test")
	err := MkdirAll("/_go_os_test/dir", 0777)
	if err != nil {
		pathErr, ok := err.(*PathError)
		// common for users not to be able to write to /
		if ok && pathErr.Err == EACCES {
			return
		}
		t.Fatalf(`MkdirAll "/_go_os_test/dir": %v`, err)
	}
	RemoveAll("/_go_os_test")
}
