package util

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/sirupsen/logrus"
)

type ExecResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

type ExecError struct {
	Command  string
	Args     []string
	ExitCode int
	Stdout   string
	Stderr   string
}

func (e *ExecError) Error() string {
	if e.Stderr != "" {
		return fmt.Sprintf("command failed: %s %s (exit code %d): %s",
			e.Command, strings.Join(e.Args, " "), e.ExitCode, e.Stderr)
	}
	return fmt.Sprintf("command failed: %s %s (exit code %d)",
		e.Command, strings.Join(e.Args, " "), e.ExitCode)
}

func RunCommand(ctx context.Context, name string, args ...string) (*ExecResult, error) {
	logrus.Debugf("Executing: %s %s", name, strings.Join(args, " "))

	cmd := exec.CommandContext(ctx, name, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	result := &ExecResult{
		Stdout: strings.TrimSpace(stdout.String()),
		Stderr: strings.TrimSpace(stderr.String()),
	}

	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			result.ExitCode = exitErr.ExitCode()
		} else {
			result.ExitCode = -1
		}

		return result, &ExecError{
			Command:  name,
			Args:     args,
			ExitCode: result.ExitCode,
			Stdout:   result.Stdout,
			Stderr:   result.Stderr,
		}
	}

	result.ExitCode = 0
	return result, nil
}

func RunCommandOutput(ctx context.Context, name string, args ...string) (string, error) {
	result, err := RunCommand(ctx, name, args...)
	if err != nil {
		return "", err
	}
	return result.Stdout, nil
}

func RunCommandQuiet(ctx context.Context, name string, args ...string) error {
	_, err := RunCommand(ctx, name, args...)
	return err
}

func CommandExists(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}
