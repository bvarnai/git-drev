package cli_test

import (
	"bytes"
	"context"
	"testing"

	"git-drev/internal/cli"
	"git-drev/internal/errcode"
	"github.com/stretchr/testify/assert"
)

func TestCompletion_Generation(t *testing.T) {
	shells := []string{"bash", "zsh", "fish", "powershell"}

	for _, sh := range shells {
		t.Run(sh, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			ctx := context.Background()

			code := cli.Run(ctx, []string{"completion", sh}, &stdout, &stderr)
			assert.Equal(t, errcode.ExitSuccess, code)
			assert.NotEmpty(t, stdout.String())
			assert.Empty(t, stderr.String())
		})
	}
}

func TestCompletion_InvalidShell(t *testing.T) {
	var stdout, stderr bytes.Buffer
	ctx := context.Background()

	code := cli.Run(ctx, []string{"completion", "invalid-shell"}, &stdout, &stderr)
	assert.Equal(t, errcode.ExitUsage, code)
	assert.Contains(t, stderr.String(), "[git-drev] Error: invalid argument \"invalid-shell\"")
}
