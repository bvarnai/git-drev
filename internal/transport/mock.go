package transport

import (
	"context"
	"io"
)

// MockSSHRunner is a configurable in-memory mock implementing SSHRunner.
type MockSSHRunner struct {
	RunFn       func(ctx context.Context, host, cmd string) (string, string, error)
	RunStreamFn func(ctx context.Context, host, cmd string, stdin io.Reader, stdoutPipe func(io.Reader) error) (string, error)
	Calls       []string
}

// Run records the call and delegates to RunFn or returns empty strings.
func (m *MockSSHRunner) Run(ctx context.Context, host, cmd string) (string, string, error) {
	m.Calls = append(m.Calls, "Run "+host+" "+cmd)
	if m.RunFn != nil {
		return m.RunFn(ctx, host, cmd)
	}
	return "", "", nil
}

// RunStream records the call and delegates to RunStreamFn or returns empty string.
func (m *MockSSHRunner) RunStream(ctx context.Context, host, cmd string, stdin io.Reader, stdoutPipe func(io.Reader) error) (string, error) {
	m.Calls = append(m.Calls, "RunStream "+host+" "+cmd)
	if m.RunStreamFn != nil {
		return m.RunStreamFn(ctx, host, cmd, stdin, stdoutPipe)
	}
	return "", nil
}

// MockRsyncRunner is a configurable in-memory mock implementing RsyncRunner.
type MockRsyncRunner struct {
	SyncFn func(ctx context.Context, opts RsyncOptions) error
	Calls  []string
}

// Sync records the call and delegates to SyncFn or returns nil.
func (m *MockRsyncRunner) Sync(ctx context.Context, opts RsyncOptions) error {
	m.Calls = append(m.Calls, "Sync "+opts.Host+" "+opts.RemotePath)
	if m.SyncFn != nil {
		return m.SyncFn(ctx, opts)
	}
	return nil
}
