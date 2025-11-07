package execx

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
)

type Result struct {
	Code int
	Err  error
}

func Run(name string, args ...string) Result {
	return RunCtx(context.Background(), name, args...)
}

func RunCtx(ctx context.Context, name string, args ...string) Result {
	if os.Getenv("DEVKIT_DEBUG") == "1" {
		fmt.Fprintf(os.Stderr, "+ %s\n", strings.Join(append([]string{name}, args...), " "))
	}
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err := cmd.Run()
	code := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else if ctx.Err() == context.DeadlineExceeded {
			code = 124
		} else {
			code = 1
		}
	}
	return Result{Code: code, Err: err}
}

// RunCtxWithOutput mirrors RunCtx but captures combined stdout/stderr while still streaming to the host.
func RunCtxWithOutput(ctx context.Context, name string, args ...string) (Result, string) {
	if os.Getenv("DEVKIT_DEBUG") == "1" {
		fmt.Fprintf(os.Stderr, "+ %s\n", strings.Join(append([]string{name}, args...), " "))
	}
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = os.Stdin
	var buf bytes.Buffer
	stdout := io.MultiWriter(os.Stdout, &buf)
	stderr := io.MultiWriter(os.Stderr, &buf)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	err := cmd.Run()
	code := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else if ctx.Err() == context.DeadlineExceeded {
			code = 124
		} else {
			code = 1
		}
	}
	return Result{Code: code, Err: err}, buf.String()
}

func WithTimeout(d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), d)
}

// RunWithInput runs a command with provided stdin content.
func RunWithInput(ctx context.Context, input []byte, name string, args ...string) Result {
	if os.Getenv("DEVKIT_DEBUG") == "1" {
		fmt.Fprintf(os.Stderr, "+ %s\n", strings.Join(append([]string{name}, args...), " "))
	}
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = io.NopCloser(strings.NewReader(string(input)))
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err := cmd.Run()
	code := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else if ctx.Err() == context.DeadlineExceeded {
			code = 124
		} else {
			code = 1
		}
	}
	return Result{Code: code, Err: err}
}

// Capture runs a command and returns stdout as string and exit code.
func Capture(ctx context.Context, name string, args ...string) (string, Result) {
	if os.Getenv("DEVKIT_DEBUG") == "1" {
		fmt.Fprintf(os.Stderr, "+ %s\n", strings.Join(append([]string{name}, args...), " "))
	}
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.Output()
	code := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else if ctx.Err() == context.DeadlineExceeded {
			code = 124
		} else {
			code = 1
		}
	}
	return string(out), Result{Code: code, Err: err}
}
