package executil

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"strings"
)

// Run streams command stdout/stderr and waits.
func Run(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	log.Printf("exec: %s %s", name, strings.Join(args, " "))
	return cmd.Run()
}

// RunCapture returns combined output.
func RunCapture(ctx context.Context, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("%s: %w: %s", name, err, string(out))
	}
	return string(out), nil
}

// Pipeline runs left | right writing to dest file. Returns when both complete.
func Pipeline(ctx context.Context, dest string, leftName string, leftArgs []string, rightName string, rightArgs []string) error {
	out, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer out.Close()

	left := exec.CommandContext(ctx, leftName, leftArgs...)
	right := exec.CommandContext(ctx, rightName, rightArgs...)
	pr, pw := io.Pipe()
	left.Stdout = pw
	left.Stderr = os.Stderr
	right.Stdin = pr
	right.Stdout = out
	right.Stderr = os.Stderr

	log.Printf("pipeline: %s %s | %s %s > %s", leftName, strings.Join(leftArgs, " "), rightName, strings.Join(rightArgs, " "), dest)

	if err := left.Start(); err != nil {
		return err
	}
	if err := right.Start(); err != nil {
		_ = left.Process.Kill()
		return err
	}

	errCh := make(chan error, 2)
	go func() {
		err := left.Wait()
		_ = pw.Close()
		errCh <- err
	}()
	go func() {
		errCh <- right.Wait()
	}()

	var first error
	for i := 0; i < 2; i++ {
		if e := <-errCh; e != nil && first == nil {
			first = e
		}
	}
	_ = pr.Close()
	return first
}

// StreamToFile runs cmd and writes stdout to dest, optionally through a filter command.
func StreamToFile(ctx context.Context, dest string, cmdName string, cmdArgs []string, filterName string, filterArgs []string) error {
	if filterName == "" {
		out, err := os.Create(dest)
		if err != nil {
			return err
		}
		defer out.Close()
		cmd := exec.CommandContext(ctx, cmdName, cmdArgs...)
		cmd.Stdout = out
		cmd.Stderr = os.Stderr
		log.Printf("exec: %s %s > %s", cmdName, strings.Join(cmdArgs, " "), dest)
		return cmd.Run()
	}
	return Pipeline(ctx, dest, cmdName, cmdArgs, filterName, filterArgs)
}

// LookOrError ensures binary exists.
func LookOrError(name string) error {
	if _, err := exec.LookPath(name); err != nil {
		return fmt.Errorf("%s not found on PATH", name)
	}
	return nil
}
