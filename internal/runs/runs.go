// Package runs starts commands that outlive lazyterminus. Drush over
// Terminus holds an SSH connection open from this machine, so if the
// terminus process dies with lazyterminus, drush on Pantheon usually stops
// mid-run. A run is started in its own session instead, with its output and
// exit code written to a directory that the next lazyterminus reads back.
//
// Each run's directory holds meta.json, output.log (stdout and stderr), and
// exit (the exit code, written when the command ends).
package runs

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Meta describes a run; it's written once when the run starts.
type Meta struct {
	Title   string    `json:"title"`
	Site    string    `json:"site"`
	Env     string    `json:"env"`
	Command string    `json:"command"` // as the history log has it
	Started time.Time `json:"started"`
}

// Run is one detached command.
type Run struct {
	Meta
	ID       string
	Dir      string
	Done     bool
	ExitCode int
	Ended    time.Time
	Seen     bool // its end has been reported to the user

	offset  int64  // how much of output.log has been read
	partial string // an unfinished last line
}

// Failed reports whether the run ended with a non-zero exit code.
func (r *Run) Failed() bool { return r.Done && r.ExitCode != 0 }

// Start launches cmd detached, in a new directory under dir. cmd is not run
// itself: its path, arguments, environment and working directory are, under
// a shell that records the exit code. Its context, stdin and stdout are
// ignored; stdin is empty, so nothing can wait on a prompt.
func Start(dir string, cmd *exec.Cmd, meta Meta) (*Run, error) {
	id, err := newID(meta.Started)
	if err != nil {
		return nil, err
	}
	rd := filepath.Join(dir, id)
	if err := os.MkdirAll(rd, 0o700); err != nil {
		return nil, err
	}
	b, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(rd, "meta.json"), b, 0o600); err != nil {
		return nil, err
	}
	out, err := os.OpenFile(filepath.Join(rd, "output.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, err
	}
	defer out.Close()

	// The exit code goes to a temp file first so a reader never sees half
	// of it.
	const script = `"$@"; code=$?; echo "$code" >"$LT_RUN_DIR/exit.tmp" && mv "$LT_RUN_DIR/exit.tmp" "$LT_RUN_DIR/exit"`
	sh := exec.Command("sh", append([]string{"-c", script, "sh", cmd.Path}, cmd.Args[1:]...)...)
	sh.Env = append(cmd.Environ(), "LT_RUN_DIR="+rd)
	sh.Dir = cmd.Dir
	sh.Stdout, sh.Stderr = out, out
	detach(sh)
	if err := sh.Start(); err != nil {
		return nil, err
	}
	go sh.Wait() //nolint:errcheck // reaped while lazyterminus is up; init adopts it after
	return &Run{Meta: meta, ID: id, Dir: rd}, nil
}

func newID(t time.Time) (string, error) {
	b := make([]byte, 3)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return t.UTC().Format("20060102T150405") + "-" + hex.EncodeToString(b), nil
}

// List returns the runs under dir, oldest first, with their state read.
// Directories it can't read are skipped.
func List(dir string) ([]*Run, error) {
	ents, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []*Run
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		r := &Run{ID: e.Name(), Dir: filepath.Join(dir, e.Name())}
		b, err := os.ReadFile(filepath.Join(r.Dir, "meta.json"))
		if err != nil || json.Unmarshal(b, &r.Meta) != nil {
			continue
		}
		r.checkDone()
		_, err = os.Stat(filepath.Join(r.Dir, "seen"))
		r.Seen = err == nil
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (r *Run) checkDone() {
	if r.Done {
		return
	}
	p := filepath.Join(r.Dir, "exit")
	b, err := os.ReadFile(p)
	if err != nil {
		return
	}
	code, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		code = -1
	}
	r.Done, r.ExitCode = true, code
	if fi, err := os.Stat(p); err == nil {
		r.Ended = fi.ModTime()
	}
}

// Poll reads output written since the last call (complete lines only) and
// reports whether the run ended since then. The exit file is checked before
// the output is read, so a finished run's last lines are never missed.
func (r *Run) Poll() (lines []string, ended bool) {
	was := r.Done
	r.checkDone()
	lines = r.readNew()
	if r.Done && r.partial != "" {
		lines, r.partial = append(lines, r.partial), ""
	}
	return lines, r.Done && !was
}

func (r *Run) readNew() []string {
	f, err := os.Open(filepath.Join(r.Dir, "output.log"))
	if err != nil {
		return nil
	}
	defer f.Close()
	if _, err := f.Seek(r.offset, io.SeekStart); err != nil {
		return nil
	}
	b, err := io.ReadAll(f)
	if err != nil || len(b) == 0 {
		return nil
	}
	r.offset += int64(len(b))
	text := r.partial + string(b)
	r.partial = ""
	if !strings.HasSuffix(text, "\n") {
		i := strings.LastIndex(text, "\n")
		r.partial, text = text[i+1:], text[:i+1]
	}
	var lines []string
	sc := bufio.NewScanner(bytes.NewReader([]byte(text)))
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	return lines
}

// SkipOutput marks the output so far as read.
func (r *Run) SkipOutput() {
	if fi, err := os.Stat(filepath.Join(r.Dir, "output.log")); err == nil {
		r.offset = fi.Size()
	}
}

// Output returns the run's whole output.
func (r *Run) Output() string {
	b, _ := os.ReadFile(filepath.Join(r.Dir, "output.log"))
	return string(b)
}

// MarkSeen records that the run's end has been reported.
func (r *Run) MarkSeen() {
	r.Seen = true
	_ = os.WriteFile(filepath.Join(r.Dir, "seen"), nil, 0o600)
}

// Prune deletes the oldest finished runs beyond keep.
func Prune(dir string, keep int) error {
	all, err := List(dir)
	if err != nil {
		return err
	}
	var done []*Run
	for _, r := range all {
		if r.Done {
			done = append(done, r)
		}
	}
	for i := 0; i < len(done)-keep; i++ {
		if err := os.RemoveAll(done[i].Dir); err != nil {
			return fmt.Errorf("pruning %s: %w", done[i].ID, err)
		}
	}
	return nil
}
