package git

import (
	"context"
	"errors"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/bvarnai/git-drev/internal/command"
	"github.com/bvarnai/git-drev/internal/domain"
	"github.com/bvarnai/git-drev/internal/errcode"
)

// CommitRecord represents a parsed Git commit from git log.
type CommitRecord struct {
	Hash      string
	ShortHash string
	Timestamp int64
	Refs      []string
	Subject   string
	Meta      *domain.RevisionMeta
}

// Inspector provides read-only repository inspection methods using Git plumbing.
type Inspector struct {
	runner Runner
}

// NewInspector creates a new Inspector backed by runner.
func NewInspector(runner Runner) *Inspector {
	return &Inspector{runner: runner}
}

// Runner returns the underlying Runner.
func (i *Inspector) Runner() Runner {
	return i.runner
}

// IsWorkTree reports whether dir is inside a non-bare Git working tree.
func (i *Inspector) IsWorkTree(ctx context.Context, dir string) (bool, error) {
	out, err := i.runner.Run(ctx, dir, "rev-parse", "--is-inside-work-tree")
	if err != nil {
		if errors.Is(err, errcode.ErrInterrupted) || errors.Is(err, errcode.ErrTransport) {
			return false, err
		}
		return false, nil
	}
	return strings.TrimSpace(out) == "true", nil
}

// RepoRoot verifies that dir is inside a non-bare Git working tree and returns
// the canonical top-level repository root and caller's relative prefix.
func (i *Inspector) RepoRoot(ctx context.Context, dir string) (root string, prefix string, err error) {
	out, runErr := i.runner.Run(ctx, dir, "rev-parse", "--is-inside-work-tree", "--show-toplevel", "--show-prefix")
	if runErr != nil {
		if errors.Is(runErr, errcode.ErrInterrupted) || errors.Is(runErr, errcode.ErrTransport) {
			return "", "", runErr
		}
		return "", "", errcode.NewError(errcode.ErrNotRepository, "Not inside a Git working tree")
	}

	lines := strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n")
	if len(lines) < 2 || strings.TrimSpace(lines[0]) != "true" {
		return "", "", errcode.NewError(errcode.ErrNotRepository, "Not inside a Git working tree")
	}

	root = strings.TrimSpace(lines[1])
	if root == "" {
		return "", "", errcode.NewError(errcode.ErrNotRepository, "Not inside a Git working tree")
	}
	root = filepath.FromSlash(root)

	if len(lines) >= 3 {
		prefix = filepath.FromSlash(strings.TrimSpace(lines[2]))
	}
	return root, prefix, nil
}

// GitDir returns the absolute path to the repository's .git directory.
func (i *Inspector) GitDir(ctx context.Context, dir string) (string, error) {
	out, runErr := i.runner.Run(ctx, dir, "rev-parse", "--git-dir")
	if runErr != nil {
		if errors.Is(runErr, errcode.ErrInterrupted) || errors.Is(runErr, errcode.ErrTransport) {
			return "", runErr
		}
		return "", errcode.NewError(errcode.ErrNotRepository, "Not inside a Git working tree")
	}
	gitDir := filepath.FromSlash(strings.TrimSpace(out))
	if gitDir == "" {
		return "", errcode.NewError(errcode.ErrNotRepository, "Unable to resolve .git directory")
	}
	if !filepath.IsAbs(gitDir) && dir != "" {
		gitDir = filepath.Join(dir, gitDir)
	}
	return filepath.Clean(gitDir), nil
}

// CurrentBranch resolves the currently checked-out branch using `git symbolic-ref --quiet --short HEAD`.
// It succeeds even on unborn branches (`git init` before the first commit).
// When HEAD is detached (exit code 1), it returns ("", true, nil).
func (i *Inspector) CurrentBranch(ctx context.Context, dir string) (branch string, detached bool, err error) {
	out, runErr := i.runner.Run(ctx, dir, "symbolic-ref", "--quiet", "--short", "HEAD")
	if runErr == nil {
		return strings.TrimSpace(out), false, nil
	}
	if errors.Is(runErr, errcode.ErrInterrupted) || errors.Is(runErr, errcode.ErrTransport) {
		return "", false, runErr
	}
	var cmdErr *command.Error
	if errors.As(runErr, &cmdErr) && cmdErr.ExitCode == 1 && strings.TrimSpace(cmdErr.Stderr) == "" {
		return "", true, nil
	}
	return "", false, errcode.WrapError(errcode.ErrGitExec, runErr, "Failed to resolve HEAD reference")
}

// HeadHash returns the 40-character SHA-1 commit hash of HEAD, or ("", nil) if HEAD is unborn.
func (i *Inspector) HeadHash(ctx context.Context, dir string) (string, error) {
	out, runErr := i.runner.Run(ctx, dir, "rev-parse", "--verify", "--quiet", "HEAD^{commit}")
	if runErr != nil {
		if errors.Is(runErr, errcode.ErrInterrupted) || errors.Is(runErr, errcode.ErrTransport) {
			return "", runErr
		}
		return "", nil
	}
	return strings.TrimSpace(out), nil
}

// HeadCommitMeta reads and parses the JSON RevisionMeta from the current HEAD commit subject.
func (i *Inspector) HeadCommitMeta(ctx context.Context, dir string) (domain.RevisionMeta, error) {
	return i.BranchCommitMeta(ctx, dir, "HEAD")
}

// BranchCommitMeta reads and parses the JSON RevisionMeta from the specified commit/branch ref's subject.
func (i *Inspector) BranchCommitMeta(ctx context.Context, dir, ref string) (domain.RevisionMeta, error) {
	out, runErr := i.runner.Run(ctx, dir, "log", "-n", "1", "--format=%s", ref, "--")
	if runErr != nil {
		if errors.Is(runErr, errcode.ErrInterrupted) || errors.Is(runErr, errcode.ErrTransport) {
			return domain.RevisionMeta{}, runErr
		}
		return domain.RevisionMeta{}, errcode.NewError(errcode.ErrGitState, "Unable to read commit metadata from %q", ref)
	}
	meta, parseErr := domain.ParseRevisionMeta(out)
	if parseErr != nil {
		return domain.RevisionMeta{}, errcode.WrapError(errcode.ErrGitState, parseErr, "Unable to read directory metadata (missing or invalid uuid)")
	}
	return meta, nil
}

// ValidateBranchName verifies that branch is a valid Git branch name under `refs/heads/<branch>`.
func (i *Inspector) ValidateBranchName(ctx context.Context, dir, branch string) error {
	trimmed := strings.TrimSpace(branch)
	if trimmed == "" || branch != trimmed || strings.HasPrefix(trimmed, "-") || trimmed == "@" || strings.EqualFold(trimmed, "HEAD") {
		return errcode.NewError(errcode.ErrUsage, "Invalid branch name '%s'", branch)
	}
	_, runErr := i.runner.Run(ctx, dir, "check-ref-format", "refs/heads/"+branch)
	if runErr != nil {
		if errors.Is(runErr, errcode.ErrInterrupted) || errors.Is(runErr, errcode.ErrTransport) {
			return runErr
		}
		return errcode.NewError(errcode.ErrUsage, "Invalid branch name '%s'", branch)
	}
	return nil
}

// LocalBranchExists checks whether `refs/heads/<branch>` exists locally via `git show-ref --verify --quiet`.
func (i *Inspector) LocalBranchExists(ctx context.Context, dir, branch string) (bool, error) {
	_, runErr := i.runner.Run(ctx, dir, "show-ref", "--verify", "--quiet", "refs/heads/"+branch)
	if runErr == nil {
		return true, nil
	}
	if errors.Is(runErr, errcode.ErrInterrupted) || errors.Is(runErr, errcode.ErrTransport) {
		return false, runErr
	}
	var cmdErr *command.Error
	if errors.As(runErr, &cmdErr) && strings.TrimSpace(cmdErr.Stderr) == "" {
		return false, nil
	}
	return false, errcode.WrapError(errcode.ErrGitExec, runErr, "Failed to verify local branch %q", branch)
}

// RemoteTrackingBranchExists checks whether `refs/remotes/origin/<branch>` exists locally.
func (i *Inspector) RemoteTrackingBranchExists(ctx context.Context, dir, branch string) (bool, error) {
	_, runErr := i.runner.Run(ctx, dir, "show-ref", "--verify", "--quiet", "refs/remotes/origin/"+branch)
	if runErr == nil {
		return true, nil
	}
	if errors.Is(runErr, errcode.ErrInterrupted) || errors.Is(runErr, errcode.ErrTransport) {
		return false, runErr
	}
	var cmdErr *command.Error
	if errors.As(runErr, &cmdErr) && strings.TrimSpace(cmdErr.Stderr) == "" {
		return false, nil
	}
	return false, errcode.WrapError(errcode.ErrGitExec, runErr, "Failed to verify remote tracking branch %q", branch)
}

// RemoteBranchExists queries origin over the network via `git ls-remote --exit-code --heads origin refs/heads/<branch>`.
// Exit code 0 -> (true, nil); exit code 2 -> (false, nil); exit code 128 or other -> ErrGitExec.
func (i *Inspector) RemoteBranchExists(ctx context.Context, dir, branch string) (bool, error) {
	ref := "refs/heads/" + branch
	_, runErr := i.runner.Run(ctx, dir, "ls-remote", "--exit-code", "--heads", "origin", ref)
	if runErr == nil {
		return true, nil
	}
	if errors.Is(runErr, errcode.ErrInterrupted) || errors.Is(runErr, errcode.ErrTransport) {
		return false, runErr
	}
	if code, ok := command.ExitCodeOf(runErr); ok && code == 2 {
		return false, nil
	}
	return false, errcode.WrapError(errcode.ErrGitExec, runErr, "Remote listing failed (git ls-remote --exit-code --heads origin %s)", ref)
}

// BranchUpstream returns the short upstream tracking branch (e.g., "origin/main") for `refs/heads/<branch>`.
func (i *Inspector) BranchUpstream(ctx context.Context, dir, branch string) (string, error) {
	out, runErr := i.runner.Run(ctx, dir, "for-each-ref", "--format=%(upstream:short)", "refs/heads/"+branch)
	if runErr != nil {
		if errors.Is(runErr, errcode.ErrInterrupted) || errors.Is(runErr, errcode.ErrTransport) {
			return "", runErr
		}
		return "", errcode.WrapError(errcode.ErrGitExec, runErr, "Failed to query upstream for branch %q", branch)
	}
	return strings.TrimSpace(out), nil
}

// FindCommitByUUID scans commits via `git log --all -z -F -i --grep=<uuid> --format=%H%x00%s` and returns the
// full 40-character hash of the first non-marker commit whose JSON subject has a matching UUID.
func (i *Inspector) FindCommitByUUID(ctx context.Context, dir, uuid string) (string, error) {
	targetUUID, ok := domain.ParseUUID(uuid)
	if !ok {
		return "", errcode.NewError(errcode.ErrGitState, "No commit was found with uuid '%s'", uuid)
	}

	out, runErr := i.runner.Run(ctx, dir, "log", "--all", "-z", "-F", "-i", "--grep="+targetUUID, "--format=%H%x00%s")
	if runErr != nil {
		if errors.Is(runErr, errcode.ErrInterrupted) || errors.Is(runErr, errcode.ErrTransport) {
			return "", runErr
		}
		return "", errcode.NewError(errcode.ErrGitState, "No commit was found with uuid '%s'", uuid)
	}

	hash := ParseCommitByUUIDOutput(out, targetUUID)
	if hash == "" {
		return "", errcode.NewError(errcode.ErrGitState, "No commit was found with uuid '%s'", uuid)
	}
	return hash, nil
}

// ParseCommitByUUIDOutput parses NUL-delimited `%H\x00%s\x00` output from `git log --all -z --format=%H%x00%s`
// and returns the first commit hash whose JSON subject matches targetUUID and is not a marker commit.
func ParseCommitByUUIDOutput(raw string, targetUUID string) string {
	fields := strings.Split(raw, "\x00")
	for idx := 0; idx+1 < len(fields); idx += 2 {
		hash := strings.TrimSpace(fields[idx])
		subject := fields[idx+1]
		if hash == "" {
			continue
		}
		meta, err := domain.ParseRevisionMeta(subject)
		if err != nil {
			continue
		}
		if meta.IsMarker() || meta.IsZeroUUID() {
			continue
		}
		if strings.EqualFold(meta.UUID, targetUUID) {
			return hash
		}
	}
	return ""
}

// ScanAllCommits executes `git log --all -z --format=%H%x00%h%x00%ad%x00%D%x00%s --date=unix`
// and parses all commit records using NUL delimiters so pipe characters or newlines in subjects
// cannot corrupt fields.
func (i *Inspector) ScanAllCommits(ctx context.Context, dir string, includeFetchHead bool) ([]CommitRecord, error) {
	args := []string{"log", "--all"}
	if includeFetchHead {
		args = append(args, "FETCH_HEAD")
	}
	args = append(args, "-z", "--format=%H%x00%h%x00%ad%x00%D%x00%s", "--date=unix")

	out, runErr := i.runner.Run(ctx, dir, args...)
	if runErr != nil {
		if errors.Is(runErr, errcode.ErrInterrupted) || errors.Is(runErr, errcode.ErrTransport) {
			return nil, runErr
		}
		return nil, nil
	}

	return ParseScanAllCommitsOutput(out), nil
}

// ParseScanAllCommitsOutput parses the NUL-delimited output of
// `git log --all -z --format=%H%x00%h%x00%ad%x00%D%x00%s --date=unix`.
func ParseScanAllCommitsOutput(raw string) []CommitRecord {
	if raw == "" {
		return nil
	}
	fields := strings.Split(raw, "\x00")
	var records []CommitRecord
	for idx := 0; idx+4 < len(fields); idx += 5 {
		hash := strings.TrimSpace(fields[idx])
		if hash == "" {
			continue
		}
		shortHash := strings.TrimSpace(fields[idx+1])
		ts, _ := strconv.ParseInt(strings.TrimSpace(fields[idx+2]), 10, 64)
		rawRefs := strings.TrimSpace(fields[idx+3])
		subject := fields[idx+4]

		var refs []string
		if rawRefs != "" {
			for _, token := range strings.Split(rawRefs, ",") {
				t := strings.TrimSpace(token)
				if t != "" {
					refs = append(refs, t)
				}
			}
		}

		var metaPtr *domain.RevisionMeta
		if m, err := domain.ParseRevisionMeta(subject); err == nil {
			metaCopy := m
			metaPtr = &metaCopy
		}

		records = append(records, CommitRecord{
			Hash:      hash,
			ShortHash: shortHash,
			Timestamp: ts,
			Refs:      refs,
			Subject:   subject,
			Meta:      metaPtr,
		})
	}
	return records
}

// ListAllBranchNames returns all deduplicated local and remote branch names.
func (i *Inspector) ListAllBranchNames(ctx context.Context, dir string) ([]string, error) {
	lines, err := i.runner.RunLines(ctx, dir, "for-each-ref", "--format=%(refname:short)", "refs/heads", "refs/remotes/origin")
	if err != nil {
		if errors.Is(err, errcode.ErrInterrupted) || errors.Is(err, errcode.ErrTransport) {
			return nil, err
		}
		return nil, nil
	}

	seen := make(map[string]bool)
	var names []string

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasSuffix(trimmed, "/HEAD") {
			continue
		}
		name := trimmed
		if strings.HasPrefix(trimmed, "origin/") {
			name = strings.TrimPrefix(trimmed, "origin/")
		}

		if !seen[name] {
			seen[name] = true
			names = append(names, name)
		}
	}
	return names, nil
}

// ListAllTags returns all tag names.
func (i *Inspector) ListAllTags(ctx context.Context, dir string) ([]string, error) {
	lines, err := i.runner.RunLines(ctx, dir, "tag", "-l")
	if err != nil {
		if errors.Is(err, errcode.ErrInterrupted) || errors.Is(err, errcode.ErrTransport) {
			return nil, err
		}
		return nil, nil
	}
	var tags []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			tags = append(tags, trimmed)
		}
	}
	return tags, nil
}

// HasCommits reports whether dir has at least one Git commit (i.e. HEAD exists).
func (i *Inspector) HasCommits(ctx context.Context, dir string) (bool, error) {
	_, err := i.runner.Run(ctx, dir, "rev-parse", "--verify", "--quiet", "HEAD")
	if err != nil {
		if errors.Is(err, errcode.ErrInterrupted) || errors.Is(err, errcode.ErrTransport) {
			return false, err
		}
		return false, nil
	}
	return true, nil
}

// HasTrackedFiles reports whether dir has any files tracked in the Git index.
func (i *Inspector) HasTrackedFiles(ctx context.Context, dir string) (bool, error) {
	out, err := i.runner.Run(ctx, dir, "ls-files")
	if err != nil {
		if errors.Is(err, errcode.ErrInterrupted) || errors.Is(err, errcode.ErrTransport) {
			return false, err
		}
		return false, nil
	}
	return strings.TrimSpace(out) != "", nil
}

// HasStagedChanges reports whether the Git index contains staged changes ready to be committed.
func (i *Inspector) HasStagedChanges(ctx context.Context, dir string) (bool, error) {
	_, err := i.runner.Run(ctx, dir, "diff", "--cached", "--quiet")
	if err == nil {
		return false, nil
	}
	if code, ok := command.ExitCodeOf(err); ok && code == 1 {
		return true, nil
	}
	if errors.Is(err, errcode.ErrInterrupted) || errors.Is(err, errcode.ErrTransport) {
		return false, err
	}
	return false, err
}
