package cli_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/bvarnai/git-drev/internal/cli"
	"github.com/bvarnai/git-drev/internal/errcode"
	"github.com/stretchr/testify/assert"
)

func TestCliRunHelp(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	ctx := context.Background()

	code := cli.Run(ctx, []string{"--help"}, &stdout, &stderr)
	assert.Equal(t, errcode.ExitSuccess, code)
	assert.Contains(t, stdout.String(), "git-drev is a Git CLI extension")
	assert.Empty(t, stderr.String())
}

func TestCliRunVersion(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	ctx := context.Background()

	code := cli.Run(ctx, []string{"--version"}, &stdout, &stderr)
	assert.Equal(t, errcode.ExitSuccess, code)
	assert.Contains(t, stdout.String(), "git-drev version")
	assert.Empty(t, stderr.String())
}

func TestCliRunUnknownFlag(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	ctx := context.Background()

	code := cli.Run(ctx, []string{"--unknown-flag"}, &stdout, &stderr)
	assert.Equal(t, errcode.ExitUsage, code)
	assert.Contains(t, stderr.String(), "[git-drev] Error: unknown flag: --unknown-flag")
}

func TestCliRunVerboseQuietConflict(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	ctx := context.Background()

	code := cli.Run(ctx, []string{"name", "-v", "-q"}, &stdout, &stderr)
	assert.Equal(t, errcode.ExitUsage, code)
	assert.Contains(t, stderr.String(), "flags --verbose and --quiet cannot be used together")
}
