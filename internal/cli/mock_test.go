package cli_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
)

type mockGitResponse struct {
	out string
	err error
}

type mockGitRunner struct {
	responses map[string]mockGitResponse
	executed  []string
}

func (m *mockGitRunner) Run(ctx context.Context, dir string, args ...string) (string, error) {
	key := strings.Join(args, " ")
	m.executed = append(m.executed, key)
	if resp, ok := m.responses[key]; ok {
		return resp.out, resp.err
	}
	for pattern, resp := range m.responses {
		if strings.HasSuffix(pattern, "*") && strings.HasPrefix(key, strings.TrimSuffix(pattern, "*")) {
			return resp.out, resp.err
		}
	}
	return "", errors.New("command not mocked: " + key)
}

func (m *mockGitRunner) RunLines(ctx context.Context, dir string, args ...string) ([]string, error) {
	out, err := m.Run(ctx, dir, args...)
	if err != nil {
		return nil, err
	}
	if out == "" {
		return []string{}, nil
	}
	return strings.Split(out, "\n"), nil
}

func (m *mockGitRunner) RunWithEnv(ctx context.Context, dir string, extraEnv []string, args ...string) (string, error) {
	return m.Run(ctx, dir, args...)
}

func (m *mockGitRunner) RunStream(ctx context.Context, dir string, stdout, stderr io.Writer, extraEnv []string, args ...string) error {
	out, err := m.RunWithEnv(ctx, dir, extraEnv, args...)
	if out != "" && stdout != nil {
		fmt.Fprintln(stdout, out)
	}
	return err
}
