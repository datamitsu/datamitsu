// Package installprocess runs installer subprocesses while retaining ownership of their process trees.
package installprocess

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
)

// Run drains only to the configured writers without retaining a second output copy.
// It stops remaining descendants before returning and waits for output draining.
func Run(ctx context.Context, cmd *exec.Cmd) error {
	_, _, err := run(ctx, cmd, nil, false)
	return err
}

// RunStreaming captures both streams; progress lines over 4 MiB are skipped until the next newline.
func RunStreaming(ctx context.Context, cmd *exec.Cmd, onStderr func(string)) (stdout, stderr string, err error) {
	out, errOut, err := run(ctx, cmd, onStderr, true)
	return out, errOut, err
}

func run(ctx context.Context, cmd *exec.Cmd, onStderr func(string), capture bool) (string, string, error) {
	if err := ctx.Err(); err != nil {
		return "", "", fmt.Errorf("run installer process: %w", err)
	}
	stdout, stderr := cmd.Stdout, cmd.Stderr
	if stdout == nil {
		stdout = io.Discard
	}
	if stderr == nil {
		stderr = io.Discard
	}
	outR, outW, err := os.Pipe()
	if err != nil {
		return "", "", fmt.Errorf("create installer stdout pipe: %w", err)
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		_ = outR.Close()
		_ = outW.Close()
		return "", "", fmt.Errorf("create installer stderr pipe: %w", err)
	}
	cmd.Cancel = nil
	cmd.WaitDelay = 0
	cmd.Stdout, cmd.Stderr = outW, errW
	process, err := start(cmd)
	if err != nil {
		_ = outR.Close()
		_ = outW.Close()
		_ = errR.Close()
		_ = errW.Close()
		return "", "", err
	}
	_ = outW.Close()
	_ = errW.Close()
	var outBuf, errBuf *bytes.Buffer
	if capture {
		outBuf, errBuf = new(bytes.Buffer), new(bytes.Buffer)
	}
	var drains sync.WaitGroup
	var outputMu sync.Mutex
	outDone, errDone := make(chan error, 1), make(chan error, 1)
	drains.Add(2)
	go func() { defer drains.Done(); outDone <- copyOutput(outR, stdout, outBuf, nil, &outputMu) }()
	go func() { defer drains.Done(); errDone <- copyOutput(errR, stderr, errBuf, onStderr, &outputMu) }()
	waitErr := finish(ctx, cmd, process)
	drains.Wait()
	waitErr = errors.Join(waitErr, <-outDone, <-errDone)
	if !capture {
		return "", "", waitErr
	}
	return outBuf.String(), errBuf.String(), waitErr
}

const maxStderrLineBytes = 4 * 1024 * 1024

func copyOutput(src *os.File, dst io.Writer, captured *bytes.Buffer, onLine func(string), outputMu *sync.Mutex) error {
	buf := make([]byte, 32*1024)
	var line bytes.Buffer
	var outputErr error
	droppingLine := false
	for {
		n, err := src.Read(buf)
		if n > 0 {
			chunk := buf[:n]
			outputMu.Lock()
			writtenErr := writeAll(dst, chunk)
			outputMu.Unlock()
			if outputErr == nil {
				outputErr = writtenErr
			}
			if captured != nil {
				_, _ = captured.Write(chunk)
			}
			if onLine != nil {
				for len(chunk) > 0 {
					newline := bytes.IndexByte(chunk, '\n')
					part := chunk
					if newline >= 0 {
						part = chunk[:newline]
					}
					if !droppingLine {
						if len(part) > maxStderrLineBytes-line.Len() {
							line.Reset()
							droppingLine = true
						} else {
							_, _ = line.Write(part)
						}
					}
					if newline < 0 {
						break
					}
					if !droppingLine {
						onLine(line.String())
					}
					line.Reset()
					droppingLine = false
					chunk = chunk[newline+1:]
				}
			}
		}
		if err != nil {
			if !errors.Is(err, io.EOF) && outputErr == nil {
				outputErr = fmt.Errorf("read child output pipe: %w", err)
			}
			break
		}
	}
	if onLine != nil && !droppingLine && line.Len() > 0 {
		onLine(line.String())
	}
	if err := src.Close(); err != nil {
		outputErr = errors.Join(outputErr, fmt.Errorf("close child output pipe: %w", err))
	}
	return outputErr
}

func writeAll(dst io.Writer, data []byte) error {
	for len(data) > 0 {
		n, err := dst.Write(data)
		if err != nil {
			return fmt.Errorf("write child output: %w", err)
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		data = data[n:]
	}
	return nil
}
