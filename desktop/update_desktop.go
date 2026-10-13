//go:build !android && !ios && !server

package main

// Installing a downloaded update on Windows, macOS and Linux. The new
// executable (or Kantra.app) is put next to the running one as
// "<name>.update"; when the app quits it takes the old one's place (a
// running file can be renamed on all three systems), so the next start runs
// the new version. If the app did not quit normally, the next start
// installs it and restarts itself.

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// installTarget: the file or bundle that gets replaced.
func installTarget() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if exe, err = filepath.EvalSymlinks(exe); err != nil {
		return "", err
	}
	if runtime.GOOS == "darwin" {
		if i := strings.Index(exe, ".app/Contents/MacOS/"); i >= 0 {
			return exe[:i+len(".app")], nil
		}
		return "", errors.New("not running from an .app bundle")
	}
	return exe, nil
}

var errNotWritable = errors.New("Нет прав на запись в папку приложения — скачайте новую версию со страницы релиза")

func platformStageUpdate(path, version string) error {
	target, err := installTarget()
	if err != nil {
		return err
	}
	return stageAt(target, path, version)
}

func stageAt(target, path, version string) error {
	var err error
	staged := target + ".update"
	os.RemoveAll(staged)
	if runtime.GOOS == "darwin" {
		err = unzipApp(path, staged)
	} else {
		err = copyFile(path, staged, 0o755)
	}
	if err != nil {
		os.RemoveAll(staged)
		if errors.Is(err, os.ErrPermission) {
			return errNotWritable
		}
		return err
	}
	os.Remove(path)
	return os.WriteFile(target+".update.ok", []byte(version), 0o644)
}

// installStaged puts the staged update in place; it reports whether it did.
func installStaged() bool {
	target, err := installTarget()
	if err != nil {
		return false
	}
	return installStagedAt(target)
}

func installStagedAt(target string) bool {
	staged, ok := target+".update", target+".update.ok"
	if _, err := os.Stat(ok); err != nil {
		return false
	}
	newApp := staged
	if runtime.GOOS == "darwin" {
		// The archive holds Kantra.app: whichever name it has.
		m, _ := filepath.Glob(filepath.Join(staged, "*.app"))
		if len(m) == 0 {
			os.RemoveAll(staged)
			os.Remove(ok)
			return false
		}
		newApp = m[0]
	}
	old := target + ".old"
	os.RemoveAll(old)
	if err := os.Rename(target, old); err != nil {
		log.Printf("update: %v", err)
		return false
	}
	if err := os.Rename(newApp, target); err != nil {
		_ = os.Rename(old, target) // put the working version back
		log.Printf("update: %v", err)
		return false
	}
	os.RemoveAll(staged)
	os.Remove(ok)
	return true
}

// earlyUpdate runs first in main: leftovers of the last update are removed,
// a pending update is installed (then the new version is started instead),
// and a restart started by "Перезапустить" waits for the old process.
func earlyUpdate() {
	for _, a := range os.Args[1:] {
		if pid, ok := strings.CutPrefix(a, "--kantra-wait="); ok {
			if n, err := strconv.Atoi(pid); err == nil {
				waitForExit(n)
			}
		}
	}
	target, err := installTarget()
	if err != nil {
		return
	}
	os.RemoveAll(target + ".old")
	if installStaged() {
		if err := relaunch(target, 0); err == nil {
			os.Exit(0)
		}
	}
}

// onQuit installs a downloaded update when the app quits.
func onQuit() { installStaged() }

func relaunch(target string, waitPID int) error {
	exe := target
	if runtime.GOOS == "darwin" {
		m, _ := filepath.Glob(filepath.Join(target, "Contents", "MacOS", "*"))
		if len(m) == 0 {
			return errors.New("no executable in the bundle")
		}
		exe = m[0]
	}
	var args []string
	for _, a := range os.Args[1:] {
		if !strings.HasPrefix(a, "--kantra-wait=") {
			args = append(args, a)
		}
	}
	if waitPID > 0 {
		args = append(args, fmt.Sprintf("--kantra-wait=%d", waitPID))
	}
	cmd := exec.Command(exe, args...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return cmd.Start()
}

// platformApplyUpdate: "Перезапустить" — install now and start the new
// version, which waits until this process has exited.
func platformApplyUpdate(string) error {
	target, err := installTarget()
	if err != nil {
		return err
	}
	if !installStaged() {
		return errors.New("Не удалось установить обновление")
	}
	if err := relaunch(target, os.Getpid()); err != nil {
		return err
	}
	application.Get().Quit()
	return nil
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// unzipApp extracts a zipped .app into dir (no paths outside it, symlinks
// kept: app bundles contain framework links).
func unzipApp(src, dir string) error {
	r, err := zip.OpenReader(src)
	if err != nil {
		return err
	}
	defer r.Close()
	root, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	for _, f := range r.File {
		p := filepath.Join(root, f.Name)
		if !strings.HasPrefix(p, root+string(os.PathSeparator)) {
			return fmt.Errorf("bad path in archive: %s", f.Name)
		}
		switch {
		case f.FileInfo().IsDir():
			if err := os.MkdirAll(p, 0o755); err != nil {
				return err
			}
		case f.Mode()&os.ModeSymlink != 0:
			rc, err := f.Open()
			if err != nil {
				return err
			}
			link, err := io.ReadAll(io.LimitReader(rc, 4096))
			rc.Close()
			if err != nil {
				return err
			}
			if filepath.IsAbs(string(link)) || !strings.HasPrefix(filepath.Join(filepath.Dir(p), string(link)), root) {
				return fmt.Errorf("bad link in archive: %s", f.Name)
			}
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				return err
			}
			if err := os.Symlink(string(link), p); err != nil {
				return err
			}
		default:
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				return err
			}
			rc, err := f.Open()
			if err != nil {
				return err
			}
			err = func() error {
				defer rc.Close()
				out, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, f.Mode().Perm()|0o600)
				if err != nil {
					return err
				}
				if _, err := io.Copy(out, rc); err != nil {
					out.Close()
					return err
				}
				return out.Close()
			}()
			if err != nil {
				return err
			}
		}
	}
	return nil
}
