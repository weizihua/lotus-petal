package stores

import (
	"bytes"
	"io"
	"io/ioutil"
	"os"
	"os/exec"
	gopath "path"
	"path/filepath"
	"strings"

	"github.com/mitchellh/go-homedir"
	"golang.org/x/xerrors"
)

func move(from, to string) error {
	from, err := homedir.Expand(from)
	if err != nil {
		return xerrors.Errorf("move: expanding from: %w", err)
	}

	to, err = homedir.Expand(to)
	if err != nil {
		return xerrors.Errorf("move: expanding to: %w", err)
	}

	if filepath.Base(from) != filepath.Base(to) {
		return xerrors.Errorf("move: base names must match ('%s' != '%s')", filepath.Base(from), filepath.Base(to))
	}

	log.Debugw("move sector data", "from", from, "to", to)

	toDir := filepath.Dir(to)

	// `mv` has decades of experience in moving files quickly; don't pretend we
	//  can do better

	var errOut bytes.Buffer
	cmd := exec.Command("/usr/bin/env", "mv", "-t", toDir, from) // nolint
	cmd.Stderr = &errOut
	if err := cmd.Run(); err != nil {
		return xerrors.Errorf("exec mv (stderr: %s): %w", strings.TrimSpace(errOut.String()), err)
	}

	return nil
}

func copySector(from, to string) error {
	if err := os.RemoveAll(to); err != nil {
		return err
	}

	info, err := os.Stat(from)
	if err != nil {
		log.Errorf("copy: can't stat %s, cause by %s", from, err)
		return err
	}

	log.Infof("copy sector data: %s -> %s", from, to)
	if info.IsDir() {
		if err := copyDir(from, to); err != nil {
			return err
		}
	} else {
		if err := copyFile(from, to); err != nil {
			return err
		}
	}

	return nil
}

func copyFile(src, dst string) error {
	var err error
	var srcfd *os.File
	var dstfd *os.File
	var srcinfo os.FileInfo

	if srcfd, err = os.Open(src); err != nil {
		return err
	}
	defer srcfd.Close()

	if dstfd, err = os.Create(dst); err != nil {
		return err
	}
	defer dstfd.Close()

	if _, err = io.Copy(dstfd, srcfd); err != nil {
		return err
	}
	if srcinfo, err = os.Stat(src); err != nil {
		return err
	}
	return os.Chmod(dst, srcinfo.Mode())
}

func copyDir(src string, dst string) error {
	var err error
	var fds []os.FileInfo
	var srcinfo os.FileInfo

	if srcinfo, err = os.Stat(src); err != nil {
		return err
	}

	if err = os.MkdirAll(dst, srcinfo.Mode()); err != nil {
		return err
	}

	if fds, err = ioutil.ReadDir(src); err != nil {
		return err
	}
	for _, fd := range fds {
		srcfp := gopath.Join(src, fd.Name())
		dstfp := gopath.Join(dst, fd.Name())

		if fd.IsDir() {
			if err = copyDir(srcfp, dstfp); err != nil {
				return err
			}
		} else {
			if err = copyFile(srcfp, dstfp); err != nil {
				return err
			}
		}
	}
	return nil
}

func GetMachineID() string {
	if fakeid, exist := os.LookupEnv("LOTUS_FAKE_MACHINE_ID"); exist {
		return fakeid
	}

	bid, err := ioutil.ReadFile("/etc/machine-id")
	if err != nil {
		log.Errorf("getting machine id failed: %+v", err)
		return ""
	}

	return strings.ReplaceAll(string(bid), "\n", "")
}
