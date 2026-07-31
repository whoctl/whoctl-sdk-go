// Package sysexec centralizes how providers run external commands. Every
// command that mutates the system goes through here, which gives us a single
// place for dry-run, logging and consistent error messages.
//
// It lives outside internal/ because running commands is what a provider does,
// and a provider in another repository has to be able to import this. That is
// also what keeps --dry-run meaning something for a provider whoctl did not
// write: the flag is enforced here, by the code the provider imports, rather
// than trusted.
package sysexec

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// Runner runs external commands.
type Runner struct {
	// DryRun makes the Runner log the command without running it.
	DryRun bool
	// Verbose writes every command to Out before running it.
	Verbose bool
	// Out receives the command log. When nil, the log is discarded.
	Out io.Writer
}

// Error describes an external command failure, keeping stderr around since it
// usually carries the useful part (`useradd: user 'x' already exists`).
type Error struct {
	Cmd    string
	Stderr string
	Err    error
}

func (e *Error) Error() string {
	msg := strings.TrimSpace(e.Stderr)
	if msg == "" {
		return fmt.Sprintf("command `%s` failed: %v", e.Cmd, e.Err)
	}
	return fmt.Sprintf("command `%s` failed: %s", e.Cmd, msg)
}

func (e *Error) Unwrap() error { return e.Err }

// Run executes name with args. Under dry-run it only logs and reports success.
func (r *Runner) Run(ctx context.Context, name string, args ...string) (string, error) {
	return r.run(ctx, nil, nil, name, args...)
}

// RunWithStdin executes name with args, writing stdin to the process standard
// input. Used by chpasswd, which takes the hash on stdin instead of argv (where
// it would be visible in `ps`).
func (r *Runner) RunWithStdin(ctx context.Context, stdin string, name string, args ...string) (string, error) {
	return r.run(ctx, strings.NewReader(stdin), nil, name, args...)
}

// RunWithEnv executes name with args and extra "KEY=value" entries added to the
// inherited environment. Package managers need it: apt-get reads
// DEBIAN_FRONTEND to decide whether it may stop and ask a question, and a
// command that blocks on a prompt nobody can answer would hang whoctl forever.
func (r *Runner) RunWithEnv(ctx context.Context, env []string, name string, args ...string) (string, error) {
	return r.run(ctx, nil, env, name, args...)
}

func (r *Runner) run(ctx context.Context, stdin io.Reader, env []string, name string, args ...string) (string, error) {
	line := formatCmd(name, args)
	if r.Verbose || r.DryRun {
		r.logf("%s %s", prefix(r.DryRun), line)
	}
	if r.DryRun {
		return "", nil
	}

	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = stdin
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}

	// Output is captured into files, not bytes.Buffer.
	//
	// A buffer makes os/exec hand the child an os.Pipe and wait for it to
	// reach EOF. Service managers spawn daemons that inherit that pipe and
	// keep it open for as long as they live, which turns `rc-service start`
	// into a call that only returns when the daemon dies, and leaves the
	// daemon holding a descriptor to a pipe nobody reads. A file has no
	// reader to wait for, so Run returns when the command itself exits.
	stdout, cleanupOut, err := tempFile()
	if err != nil {
		return "", err
	}
	defer cleanupOut()
	stderr, cleanupErr, err := tempFile()
	if err != nil {
		return "", err
	}
	defer cleanupErr()

	cmd.Stdout = stdout
	cmd.Stderr = stderr
	runErr := cmd.Run()

	out := readAll(stdout)
	if runErr != nil {
		return out, &Error{Cmd: line, Stderr: readAll(stderr), Err: runErr}
	}
	return out, nil
}

// tempFile creates a scratch file for a command's output and returns a closure
// that removes it.
func tempFile() (*os.File, func(), error) {
	f, err := os.CreateTemp("", "whoctl-out-*")
	if err != nil {
		return nil, nil, fmt.Errorf("creating a temporary file for command output: %w", err)
	}
	return f, func() {
		name := f.Name()
		f.Close()
		os.Remove(name)
	}, nil
}

func readAll(f *os.File) string {
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return ""
	}
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, f); err != nil {
		return ""
	}
	return buf.String()
}

// Mutate reports whether a mutation that is not an external command — a file
// rewritten in place, say — should actually happen. It logs the change the same
// way commands are logged, and returns false under dry-run so the caller skips
// it.
func (r *Runner) Mutate(description string) bool {
	if r.Verbose || r.DryRun {
		r.logf("%s %s", prefix(r.DryRun), description)
	}
	return !r.DryRun
}

// Which returns the path of an executable in PATH, or "" when it is missing.
func Which(name string) string {
	path, err := exec.LookPath(name)
	if err != nil {
		return ""
	}
	return path
}

func (r *Runner) logf(format string, args ...any) {
	if r.Out == nil {
		return
	}
	fmt.Fprintf(r.Out, format+"\n", args...)
}

func prefix(dryRun bool) string {
	if dryRun {
		return "[dry-run]"
	}
	return "[exec]"
}

func formatCmd(name string, args []string) string {
	parts := make([]string, 0, len(args)+1)
	parts = append(parts, name)
	for _, a := range args {
		if strings.ContainsAny(a, " \t\"'") {
			a = fmt.Sprintf("%q", a)
		}
		parts = append(parts, a)
	}
	return strings.Join(parts, " ")
}
