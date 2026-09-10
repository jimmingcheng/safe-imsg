package backend

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"os/exec"
	"time"
)

// Each stream has one reader and one Wait owner. Close cancels the process,
// closes inherited pipes, and joins the reader before returning.
type processStream struct {
	cmd    *exec.Cmd
	stdout io.ReadCloser
	events chan streamEvent
	done   chan struct{}
	cancel context.CancelFunc
}

type streamEvent struct {
	line    []byte
	readErr error
	exitErr error
}

func backendCommand(ctx context.Context, path string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Dir = "/"
	cmd.Stderr = io.Discard
	// Bounds os/exec's pipe-copy goroutines if a descendant inherits stderr.
	cmd.WaitDelay = 250 * time.Millisecond
	configureProcess(cmd)
	return cmd
}

func startStream(ctx context.Context, path string, args []string) (*processStream, error) {
	ctx, cancel := context.WithCancel(ctx)
	cmd := backendCommand(ctx, path, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, ErrFailed
	}
	if err := cmd.Start(); err != nil {
		cancel()
		_ = stdout.Close()
		return nil, ErrFailed
	}
	s := &processStream{cmd: cmd, stdout: stdout, cancel: cancel, events: make(chan streamEvent), done: make(chan struct{})}
	go func() {
		defer close(s.done)
		defer close(s.events)
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 64*1024), maxBackendLineBytes)
		total := 0
		var readErr error
	read:
		for scanner.Scan() {
			// Include blank lines in the resource bound too.
			total += len(scanner.Bytes()) + 1
			if total > maxBackendOutputBytes {
				readErr = ErrOverflow
				break
			}
			if len(bytes.TrimSpace(scanner.Bytes())) == 0 {
				continue
			}
			select {
			case s.events <- streamEvent{line: bytes.Clone(scanner.Bytes())}:
			case <-ctx.Done():
				break read
			}
		}
		if readErr == nil {
			readErr = scanner.Err()
		}
		if readErr != nil || ctx.Err() != nil {
			_ = cmd.Cancel()
		}
		exitErr := cmd.Wait()
		select {
		case s.events <- streamEvent{readErr: readErr, exitErr: exitErr}:
		case <-ctx.Done():
		}
	}()
	return s, nil
}

func (s *processStream) close() {
	s.cancel()
	_ = s.stdout.Close()
	<-s.done
}
