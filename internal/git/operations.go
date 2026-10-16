package git

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"git-drev/internal/errcode"
)

// WildcardFetchRefspec is the canonical origin fetch refspec required so all
// remote branches are visible to git-drev commands.
const WildcardFetchRefspec = "+refs/heads/*:refs/remotes/origin/*"

// Operations provides safe Git mutating methods.
type Operations struct {
	runner Runner
}

// NewOperations creates a new Operations instance backed by runner.
func NewOperations(runner Runner) *Operations {
	return &Operations{runner: runner}
}

// EnsureRemoteFetchConfig inspects `git config --local --get-all remote.origin.fetch`
// read-only and idempotently adds `+refs/heads/*:refs/remotes/origin/*` only if missing.
func (o *Operations) EnsureRemoteFetchConfig(ctx context.Context, dir string) error {
	lines, runErr := o.runner.RunLines(ctx, dir, "config", "--local", "--get-all", "remote.origin.fetch")
	if runErr != nil {
		if errors.Is(runErr, errcode.ErrInterrupted) || errors.Is(runErr, errcode.ErrTransport) {
			return runErr
		}
		if code, ok := ExitCodeOf(runErr); !ok || code != 1 {
			return errcode.WrapError(errcode.ErrGitExec, runErr, "Failed to read remote.origin.fetch")
		}
	}

	for _, line := range lines {
		if strings.TrimSpace(line) == WildcardFetchRefspec {
			return nil
		}
	}

	_, addErr := o.runner.Run(ctx, dir, "config", "--local", "--add", "remote.origin.fetch", WildcardFetchRefspec)
	if addErr != nil {
		if errors.Is(addErr, errcode.ErrInterrupted) || errors.Is(addErr, errcode.ErrTransport) {
			return addErr
		}
		return errcode.WrapError(errcode.ErrGitExec, addErr, "Failed to configure remote.origin.fetch")
	}
	return nil
}

// FetchAll executes `git fetch --all --prune` in dir.
func (o *Operations) FetchAll(ctx context.Context, dir string, quiet bool) error {
	args := []string{"fetch", "--all", "--prune"}
	if quiet {
		args = append(args, "--quiet")
	}

	if _, runErr := o.runner.Run(ctx, dir, args...); runErr != nil {
		if errors.Is(runErr, errcode.ErrInterrupted) || errors.Is(runErr, errcode.ErrTransport) {
			return runErr
		}
		return errcode.WrapError(errcode.ErrGitExec, runErr, "Fetch failed (git fetch --all --prune)")
	}
	return nil
}

// Checkout executes `git [-c advice.detachedHead=false] checkout <ref>` in dir.
func (o *Operations) Checkout(ctx context.Context, dir, ref string, detachAdvice bool) error {
	var args []string
	if !detachAdvice {
		args = append(args, "-c", "advice.detachedHead=false")
	}
	args = append(args, "checkout", ref)

	if _, runErr := o.runner.Run(ctx, dir, args...); runErr != nil {
		if errors.Is(runErr, errcode.ErrInterrupted) || errors.Is(runErr, errcode.ErrTransport) {
			return runErr
		}
		return errcode.WrapError(errcode.ErrGitExec, runErr, "Checkout failed (git checkout %s)", ref)
	}
	return nil
}

// CheckoutNewBranch executes `git checkout -b <branch>` in dir.
func (o *Operations) CheckoutNewBranch(ctx context.Context, dir, branch string) error {
	if _, runErr := o.runner.Run(ctx, dir, "checkout", "-b", branch); runErr != nil {
		if errors.Is(runErr, errcode.ErrInterrupted) || errors.Is(runErr, errcode.ErrTransport) {
			return runErr
		}
		return errcode.WrapError(errcode.ErrGitExec, runErr, "Checkout failed (git checkout -b %s)", branch)
	}
	return nil
}

// Switch executes `git switch <branch>` in dir.
func (o *Operations) Switch(ctx context.Context, dir, branch string) error {
	if _, runErr := o.runner.Run(ctx, dir, "switch", branch); runErr != nil {
		if errors.Is(runErr, errcode.ErrInterrupted) || errors.Is(runErr, errcode.ErrTransport) {
			return runErr
		}
		return errcode.WrapError(errcode.ErrGitExec, runErr, "Switch failed (git switch %s)", branch)
	}
	return nil
}

// DeleteLocalBranch executes `git branch -D <branch>` in dir.
func (o *Operations) DeleteLocalBranch(ctx context.Context, dir, branch string) error {
	if _, runErr := o.runner.Run(ctx, dir, "branch", "-D", branch); runErr != nil {
		if errors.Is(runErr, errcode.ErrInterrupted) || errors.Is(runErr, errcode.ErrTransport) {
			return runErr
		}
		return errcode.WrapError(errcode.ErrGitExec, runErr, "Failed to delete local branch %s", branch)
	}
	return nil
}

// ResetHard executes `git reset --hard <ref> --` in dir.
func (o *Operations) ResetHard(ctx context.Context, dir, ref string) error {
	if _, runErr := o.runner.Run(ctx, dir, "reset", "--hard", ref, "--"); runErr != nil {
		if errors.Is(runErr, errcode.ErrInterrupted) || errors.Is(runErr, errcode.ErrTransport) {
			return runErr
		}
		return errcode.WrapError(errcode.ErrGitExec, runErr, "Reset failed")
	}
	return nil
}

// StageAll executes `git add --all` in dir.
func (o *Operations) StageAll(ctx context.Context, dir string) error {
	if _, runErr := o.runner.Run(ctx, dir, "add", "--all"); runErr != nil {
		if errors.Is(runErr, errcode.ErrInterrupted) || errors.Is(runErr, errcode.ErrTransport) {
			return runErr
		}
		return errcode.WrapError(errcode.ErrGitExec, runErr, "Staging failed (git add --all)")
	}
	return nil
}

// CommitAllowEmpty executes `git commit --allow-empty -m <message>` in dir.
func (o *Operations) CommitAllowEmpty(ctx context.Context, dir, message string) error {
	if _, runErr := o.runner.Run(ctx, dir, "commit", "--allow-empty", "-m", message); runErr != nil {
		if errors.Is(runErr, errcode.ErrInterrupted) || errors.Is(runErr, errcode.ErrTransport) {
			return runErr
		}
		return errcode.WrapError(errcode.ErrGitExec, runErr, "Commit failed (git commit --allow-empty)")
	}
	return nil
}

// RebaseTheirs executes `git rebase --strategy-option=theirs <upstream>` in dir.
func (o *Operations) RebaseTheirs(ctx context.Context, dir, upstream string) error {
	if _, runErr := o.runner.Run(ctx, dir, "rebase", "--strategy-option=theirs", upstream); runErr != nil {
		if errors.Is(runErr, errcode.ErrInterrupted) || errors.Is(runErr, errcode.ErrTransport) {
			return runErr
		}
		return errcode.WrapError(errcode.ErrGitExec, runErr, "Rebase failed")
	}
	return nil
}

// RebaseAbort executes `git rebase --abort` in dir.
func (o *Operations) RebaseAbort(ctx context.Context, dir string) error {
	_, runErr := o.runner.Run(ctx, dir, "rebase", "--abort")
	return runErr
}

// PushForceWithLease executes `git push [--set-upstream] --force-with-lease origin refs/heads/<branch>`
// in dir and reports whether a push failure was due to a stale ref (`stale info`).
func (o *Operations) PushForceWithLease(ctx context.Context, dir, branch string, setUpstream bool) (stale bool, output string, err error) {
	args := []string{"push"}
	if setUpstream {
		args = append(args, "--set-upstream")
	}
	args = append(args, "--force-with-lease", "origin", "refs/heads/"+branch)

	out, runErr := o.runner.Run(ctx, dir, args...)
	if runErr != nil {
		if errors.Is(runErr, errcode.ErrInterrupted) || errors.Is(runErr, errcode.ErrTransport) {
			return false, out, runErr
		}
		var cmdErr *CommandError
		if errors.As(runErr, &cmdErr) {
			output = cmdErr.CombinedOutput()
		} else {
			output = runErr.Error()
		}
		isStale := IsStalePushOutput(output)
		cmdStr := fmt.Sprintf("git push --force-with-lease origin refs/heads/%s", branch)
		if setUpstream {
			cmdStr = fmt.Sprintf("git push --set-upstream origin %s --force-with-lease", branch)
		}
		return isStale, output, errcode.NewError(errcode.ErrGitExec, "Push failed (%s): %s", cmdStr, output)
	}
	return false, out, nil
}

// IsStalePushOutput reports whether git push output (with LC_ALL=C) indicates a
// `--force-with-lease` rejection due to concurrent remote updates (`stale info`).
func IsStalePushOutput(output string) bool {
	return strings.Contains(strings.ToLower(output), "stale info")
}
