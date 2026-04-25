package tools

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"
)

type SessionManager struct {
	mu       sync.Mutex
	nextID   int
	sessions map[int]*ExecSession
	doneTTL  time.Duration
}

type ExecSession struct {
	id      int
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	buf     streamBuffer
	done    chan struct{}
	started time.Time
	streams sync.WaitGroup

	mu       sync.Mutex
	exitCode *int
	waitErr  string
	doneAt   time.Time
}

type streamBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func NewSessionManager() *SessionManager {
	m := &SessionManager{
		nextID:   1,
		sessions: map[int]*ExecSession{},
		doneTTL:  5 * time.Minute,
	}
	go m.reapDoneSessions()
	return m
}

func (m *SessionManager) Start(command []string, workdir string, timeoutMS int) (*ExecSession, error) {
	if len(command) == 0 || command[0] == "" {
		return nil, fmt.Errorf("command is required")
	}
	ctx := context.Background()
	var cancel context.CancelFunc
	if timeoutMS > 0 {
		ctx, cancel = context.WithTimeout(ctx, time.Duration(timeoutMS)*time.Millisecond)
	}
	cmd := exec.CommandContext(ctx, command[0], command[1:]...)
	cmd.Dir = workdir
	cmd.Env = os.Environ()
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		if cancel != nil {
			cancel()
		}
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		if cancel != nil {
			cancel()
		}
		return nil, err
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		if cancel != nil {
			cancel()
		}
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		if cancel != nil {
			cancel()
		}
		return nil, err
	}

	m.mu.Lock()
	id := m.nextID
	m.nextID++
	s := &ExecSession{id: id, cmd: cmd, stdin: stdin, done: make(chan struct{}), started: time.Now()}
	m.sessions[id] = s
	m.mu.Unlock()

	s.streams.Add(2)
	go s.copyStream("stdout", stdout)
	go s.copyStream("stderr", stderr)
	go func() {
		if cancel != nil {
			defer cancel()
		}
		waitErr := cmd.Wait()
		exit := 0
		if waitErr != nil {
			exit = -1
			var exitErr *exec.ExitError
			if errors.As(waitErr, &exitErr) {
				exit = exitErr.ExitCode()
			}
		}
		s.streams.Wait()
		s.mu.Lock()
		s.exitCode = &exit
		if waitErr != nil {
			s.waitErr = waitErr.Error()
		}
		s.doneAt = time.Now()
		s.mu.Unlock()
		close(s.done)
	}()
	return s, nil
}

func (m *SessionManager) Get(id int) (*ExecSession, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[id]
	return s, ok
}

func (m *SessionManager) Remove(id int) {
	m.mu.Lock()
	delete(m.sessions, id)
	m.mu.Unlock()
}

func (m *SessionManager) reapDoneSessions() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		cutoff := time.Now().Add(-m.doneTTL)
		m.mu.Lock()
		for id, session := range m.sessions {
			session.mu.Lock()
			doneAt := session.doneAt
			session.mu.Unlock()
			if !doneAt.IsZero() && doneAt.Before(cutoff) {
				delete(m.sessions, id)
			}
		}
		m.mu.Unlock()
	}
}

func (s *ExecSession) Write(chars string) error {
	if chars == "" {
		return nil
	}
	_, err := io.WriteString(s.stdin, chars)
	return err
}

func (s *ExecSession) WaitOrYield(yieldMS int) bool {
	if yieldMS < 0 {
		yieldMS = 0
	}
	select {
	case <-s.done:
		return true
	case <-time.After(time.Duration(yieldMS) * time.Millisecond):
		return false
	}
}

func (s *ExecSession) Snapshot() (output string, exitCode *int, waitErr string, wall float64) {
	s.mu.Lock()
	exitCode = s.exitCode
	waitErr = s.waitErr
	s.mu.Unlock()
	return s.buf.drain(), exitCode, waitErr, time.Since(s.started).Seconds()
}

func (s *ExecSession) copyStream(name string, r io.Reader) {
	defer s.streams.Done()
	_, _ = io.Copy(&prefixedWriter{prefix: name, target: &s.buf}, r)
}

type prefixedWriter struct {
	prefix string
	target *streamBuffer
}

func (w *prefixedWriter) Write(p []byte) (int, error) {
	w.target.write("[" + w.prefix + "] " + string(p))
	return len(p), nil
}

func (b *streamBuffer) write(s string) {
	b.mu.Lock()
	_, _ = b.buf.WriteString(s)
	b.mu.Unlock()
}

func (b *streamBuffer) drain() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := b.buf.String()
	b.buf.Reset()
	return out
}
