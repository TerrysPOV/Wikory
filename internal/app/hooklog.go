package app

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/TerrysPOV/Wikory/internal/store"
)

const (
	// hookLogMax is the size past which the next write first trims the log.
	hookLogMax = 1 << 20
	// hookLogKeep is how much of the newest log is kept when it is trimmed.
	hookLogKeep = 256 << 10
	// hookLogWindow is how far back doctor and the briefing count hook errors.
	hookLogWindow = 24 * time.Hour
)

func hookLogPath(env *Env) (string, error) {
	dir, err := store.DataDir(env.Getenv)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "hook-errors.log"), nil
}

// logHookError records the kind of a hook failure and the time, never its content. The
// log is kept small: past hookLogMax only the newest hookLogKeep bytes are kept.
func logHookError(env *Env, kind string) {
	dir, err := store.DataDir(env.Getenv)
	if err != nil {
		return
	}
	store.RestrictUmask()
	if store.EnsurePrivateDir(dir) != nil {
		return
	}
	path := filepath.Join(dir, "hook-errors.log")
	trimHookLog(path)
	f, err := openHookLog(path) // never blocks; regular files only
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s %s\n", time.Now().UTC().Format(time.RFC3339), kind)
}

// trimHookLog keeps the newest hookLogKeep bytes (from a line boundary) of a log that has
// grown past hookLogMax. It is best effort: a hook that loses the race to another loses at most
// a few lines, and a log that cannot be trimmed is left alone.
func trimHookLog(path string) {
	rf, err := openHookLogRead(path)
	if err != nil {
		return
	}
	defer rf.Close()
	fi, err := rf.Stat()
	if err != nil || fi.Size() <= hookLogMax {
		return
	}
	buf := make([]byte, hookLogKeep)
	n, err := rf.ReadAt(buf, fi.Size()-hookLogKeep)
	if err != nil && err != io.EOF {
		return
	}
	buf = buf[:n]
	if i := bytes.IndexByte(buf, '\n'); i >= 0 {
		buf = buf[i+1:]
	}
	tmp := path + ".trim"
	_ = os.Remove(tmp)
	out, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return
	}
	_, werr := out.Write(buf)
	cerr := out.Close()
	if werr != nil || cerr != nil || os.Rename(tmp, path) != nil {
		_ = os.Remove(tmp)
	}
}

// hookLogReport is what doctor and the briefing know about the hook error log.
type hookLogReport struct {
	Recent  int    // errors logged in the last hookLogWindow
	Problem string // non-empty when the log exists but hooks cannot use it
}

// readHookLog counts the entries of the last 24 hours, reading at most the newest hookLogMax
// bytes of a regular file, and reports a log that exists but cannot be used (a symlink, a
// FIFO or other non-regular file, or one hooks cannot append to).
func readHookLog(env *Env, now time.Time) hookLogReport {
	path, err := hookLogPath(env)
	if err != nil {
		return hookLogReport{}
	}
	fi, err := os.Lstat(path)
	if err != nil {
		return hookLogReport{} // no log, no hook has failed
	}
	if !fi.Mode().IsRegular() {
		return hookLogReport{Problem: "the hook error log " + path + " is not a regular file (" + kindOfFile(fi.Mode()) + "), so hooks cannot record their failures there"}
	}
	var rep hookLogReport
	if f, err := openHookLogRead(path); err != nil {
		rep.Problem = "the hook error log " + path + " cannot be read: " + err.Error()
	} else {
		defer f.Close()
		size := int64(0)
		if st, err := f.Stat(); err == nil {
			size = st.Size()
		}
		start := max(size-hookLogMax, 0)
		b, _ := io.ReadAll(io.NewSectionReader(f, start, hookLogMax))
		if start > 0 {
			if i := bytes.IndexByte(b, '\n'); i >= 0 {
				b = b[i+1:] // the first line is cut
			}
		}
		rep.Recent = countRecentLines(b, now)
	}
	if rep.Problem == "" {
		if w, err := openHookLog(path); err != nil {
			rep.Problem = "the hook error log " + path + " cannot be written, so hooks cannot record their failures: " + err.Error()
		} else {
			w.Close()
		}
	}
	return rep
}

// countRecentLines counts lines of the form "<RFC 3339 time> <kind>" no older than the window.
func countRecentLines(b []byte, now time.Time) int {
	n := 0
	for _, line := range bytes.Split(b, []byte{'\n'}) {
		ts, _, ok := bytes.Cut(line, []byte{' '})
		if !ok {
			continue
		}
		at, err := time.Parse(time.RFC3339, string(ts))
		if err == nil && now.Sub(at) <= hookLogWindow {
			n++
		}
	}
	return n
}

func kindOfFile(m os.FileMode) string {
	switch {
	case m&os.ModeSymlink != 0:
		return "a symlink"
	case m&os.ModeNamedPipe != 0:
		return "a FIFO"
	case m.IsDir():
		return "a directory"
	}
	return "a special file"
}
