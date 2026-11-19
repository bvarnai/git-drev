package cli_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bvarnai/git-drev/internal/cli"
	"github.com/bvarnai/git-drev/internal/domain"
	"github.com/bvarnai/git-drev/internal/errcode"
	"github.com/bvarnai/git-drev/internal/git"
	"github.com/bvarnai/git-drev/internal/storage"
	"github.com/bvarnai/git-drev/internal/term"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUsageCommand_UnitTable(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, ".git-drev.yaml")
	cfgContent := `
workdir: data
remote:
  host: backup.example.com
  path: /storage/myrepo
`
	require.NoError(t, os.WriteFile(configPath, []byte(cfgContent), 0o644))

	tests := []struct {
		name         string
		args         []string
		mockGit      map[string]mockGitResponse
		mockStorage  *storage.MockManager
		expectedCode int
		errSubstr    string
		validateOut  func(t *testing.T, stdout string, stderr string)
	}{
		{
			name:         "excess arguments returns usage error",
			args:         []string{"usage", "extra"},
			expectedCode: errcode.ExitUsage,
			errSubstr:    "usage takes no arguments",
		},
		{
			name: "outside worktree returns code 3",
			args: []string{"usage"},
			mockGit: map[string]mockGitResponse{
				"rev-parse --is-inside-work-tree --show-toplevel --show-prefix": {out: "false\n\n"},
			},
			expectedCode: errcode.ExitConfig,
			errSubstr:    "Not inside a Git working tree",
		},
		{
			name: "remote path not found returns ExitRemoteNotFound",
			args: []string{"usage"},
			mockGit: map[string]mockGitResponse{
				"rev-parse --is-inside-work-tree --show-toplevel --show-prefix": {out: "true\n" + tempDir + "\n"},
				"config --local --get-all remote.origin.fetch":                  {out: git.WildcardFetchRefspec},
			},
			mockStorage: &storage.MockManager{
				QueryUsageFn: func(ctx context.Context, host, basePath string, opts ...storage.QueryUsageOptions) (string, []domain.RemoteRevision, error) {
					return "", nil, errcode.NewError(errcode.ErrRemoteNotFound, "remote path does not exist")
				},
			},
			expectedCode: errcode.ExitRemoteNotFound,
			errSubstr:    "remote path does not exist",
		},
		{
			name: "summary mode outputs total size and hint",
			args: []string{"usage"},
			mockGit: map[string]mockGitResponse{
				"rev-parse --is-inside-work-tree --show-toplevel --show-prefix": {out: "true\n" + tempDir + "\n"},
				"config --local --get-all remote.origin.fetch":                  {out: git.WildcardFetchRefspec},
			},
			mockStorage: &storage.MockManager{
				QueryUsageFn: func(ctx context.Context, host, basePath string, opts ...storage.QueryUsageOptions) (string, []domain.RemoteRevision, error) {
					return "1.5G", []domain.RemoteRevision{
						{UUID: "550e8400-e29b-41d4-a716-446655440000", SizeHuman: "1.5G"},
					}, nil
				},
			},
			expectedCode: errcode.ExitSuccess,
			validateOut: func(t *testing.T, stdout string, stderr string) {
				assert.Contains(t, stdout, "Remote storage:    backup.example.com:/storage/myrepo")
				assert.Contains(t, stdout, "Total space used:  1.5G")
				assert.Contains(t, stdout, "Revisions found:   1")
				assert.Contains(t, stderr, "Retrieved storage usage from host 'backup.example.com'")
				assert.Contains(t, stderr, "Hint: Use -a/--all")
			},
		},
		{
			name: "all flag correlates commits and marks orphaned without debug spam",
			args: []string{"usage", "-a"},
			mockGit: map[string]mockGitResponse{
				"rev-parse --is-inside-work-tree --show-toplevel --show-prefix": {out: "true\n" + tempDir + "\n"},
				"config --local --get-all remote.origin.fetch":                  {out: git.WildcardFetchRefspec},
				"log --all -z --format=%H%x00%h%x00%ad%x00%D%x00%s --date=unix": {
					out: "1111111111111111111111111111111111111111\x001111111\x001700000000\x00HEAD -> main, origin/main\x00" + `{"uuid":"550e8400-e29b-41d4-a716-446655440000","seq":"10"}` + "\x00",
				},
			},
			mockStorage: &storage.MockManager{
				QueryUsageFn: func(ctx context.Context, host, basePath string, opts ...storage.QueryUsageOptions) (string, []domain.RemoteRevision, error) {
					return "2.0G", []domain.RemoteRevision{
						{UUID: "550e8400-e29b-41d4-a716-446655440000", SizeHuman: "1.0G", ModTime: time.Unix(1700000000, 0)},
						{UUID: "c1ca82b1-7f34-4f4c-9a76-05e3297b2a23", SizeHuman: "1.0G", ModTime: time.Unix(1700001000, 0)},
					}, nil
				},
			},
			expectedCode: errcode.ExitSuccess,
			validateOut: func(t *testing.T, stdout string, stderr string) {
				assert.Contains(t, stdout, "Total space used:  2.0G")
				assert.Contains(t, stdout, "Revisions breakdown:")
				assert.NotContains(t, stderr, "[debug]")
				// Active revision
				assert.Contains(t, stdout, "550e8400-e29b-41d4-a716-446655440000")
				assert.Contains(t, stdout, "1111111")
				assert.Contains(t, stdout, "main, origin/main")
				assert.Contains(t, stdout, "seq: 10")
				// Orphaned revision
				assert.Contains(t, stdout, "c1ca82b1-7f34-4f4c-9a76-05e3297b2a23")
				assert.Contains(t, stdout, "[orphaned]")
			},
		},
		{
			name: "verbose mode keeps summary on stdout and emits debug logs to stderr",
			args: []string{"usage", "-v"},
			mockGit: map[string]mockGitResponse{
				"rev-parse --is-inside-work-tree --show-toplevel --show-prefix": {out: "true\n" + tempDir + "\n"},
				"config --local --get-all remote.origin.fetch":                  {out: git.WildcardFetchRefspec},
			},
			mockStorage: &storage.MockManager{
				QueryUsageFn: func(ctx context.Context, host, basePath string, opts ...storage.QueryUsageOptions) (string, []domain.RemoteRevision, error) {
					return "2.0G", []domain.RemoteRevision{
						{UUID: "550e8400-e29b-41d4-a716-446655440000", SizeHuman: "1.0G", ModTime: time.Unix(1700000000, 0)},
						{UUID: "c1ca82b1-7f34-4f4c-9a76-05e3297b2a23", SizeHuman: "1.0G", ModTime: time.Unix(1700001000, 0)},
					}, nil
				},
			},
			expectedCode: errcode.ExitSuccess,
			validateOut: func(t *testing.T, stdout string, stderr string) {
				assert.Contains(t, stdout, "Total space used:  2.0G")
				assert.Contains(t, stdout, "Revisions found:   2")
				assert.NotContains(t, stdout, "Revisions breakdown:")
				assert.Contains(t, stderr, "[git-drev] Debug: Querying remote storage on host 'backup.example.com'")
				assert.Contains(t, stderr, "[git-drev] Debug: Storage discovery completed: 2 revision(s) found")
			},
		},
		{
			name: "all flag filters grafted decoration token on shallow commits",
			args: []string{"usage", "-a"},
			mockGit: map[string]mockGitResponse{
				"rev-parse --is-inside-work-tree --show-toplevel --show-prefix": {out: "true\n" + tempDir + "\n"},
				"config --local --get-all remote.origin.fetch":                  {out: git.WildcardFetchRefspec},
				"log --all -z --format=%H%x00%h%x00%ad%x00%D%x00%s --date=unix": {
					out: "1111111111111111111111111111111111111111\x001111111\x001700000000\x00grafted\x00" + `{"uuid":"550e8400-e29b-41d4-a716-446655440000","seq":"10"}` + "\x00" +
						"2222222222222222222222222222222222222222\x002222222\x001700001000\x00grafted, HEAD -> main, origin/main\x00" + `{"uuid":"c1ca82b1-7f34-4f4c-9a76-05e3297b2a23","seq":"11"}` + "\x00",
				},
			},
			mockStorage: &storage.MockManager{
				QueryUsageFn: func(ctx context.Context, host, basePath string, opts ...storage.QueryUsageOptions) (string, []domain.RemoteRevision, error) {
					return "2.0G", []domain.RemoteRevision{
						{UUID: "550e8400-e29b-41d4-a716-446655440000", SizeHuman: "1.0G", ModTime: time.Unix(1700000000, 0)},
						{UUID: "c1ca82b1-7f34-4f4c-9a76-05e3297b2a23", SizeHuman: "1.0G", ModTime: time.Unix(1700001000, 0)},
					}, nil
				},
			},
			expectedCode: errcode.ExitSuccess,
			validateOut: func(t *testing.T, stdout string, stderr string) {
				assert.NotContains(t, stdout, "grafted")
				assert.Contains(t, stdout, "1111111     -                     seq: 10")
				assert.Contains(t, stdout, "2222222     main, origin/main     seq: 11")
			},
		},
		{
			name: "all and verbose combined renders table and emits debug logs",
			args: []string{"usage", "-a", "-v"},
			mockGit: map[string]mockGitResponse{
				"rev-parse --is-inside-work-tree --show-toplevel --show-prefix": {out: "true\n" + tempDir + "\n"},
				"config --local --get-all remote.origin.fetch":                  {out: git.WildcardFetchRefspec},
				"log --all -z --format=%H%x00%h%x00%ad%x00%D%x00%s --date=unix": {
					out: "1111111111111111111111111111111111111111\x001111111\x001700000000\x00HEAD -> main, origin/main\x00" + `{"uuid":"550e8400-e29b-41d4-a716-446655440000","seq":"10"}` + "\x00",
				},
			},
			mockStorage: &storage.MockManager{
				QueryUsageFn: func(ctx context.Context, host, basePath string, opts ...storage.QueryUsageOptions) (string, []domain.RemoteRevision, error) {
					return "2.0G", []domain.RemoteRevision{
						{UUID: "550e8400-e29b-41d4-a716-446655440000", SizeHuman: "1.0G", ModTime: time.Unix(1700000000, 0)},
						{UUID: "c1ca82b1-7f34-4f4c-9a76-05e3297b2a23", SizeHuman: "1.0G", ModTime: time.Unix(1700001000, 0)},
					}, nil
				},
			},
			expectedCode: errcode.ExitSuccess,
			validateOut: func(t *testing.T, stdout string, stderr string) {
				assert.Contains(t, stdout, "Total space used:  2.0G")
				assert.Contains(t, stdout, "Revisions breakdown:")
				assert.Contains(t, stdout, "550e8400-e29b-41d4-a716-446655440000")
				assert.Contains(t, stdout, "[orphaned]")
				assert.Contains(t, stderr, "[git-drev] Debug: Querying remote storage on host 'backup.example.com'")
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			gitRunner := &mockGitRunner{responses: tt.mockGit}
			app := cli.NewApp(&stdout, &stderr, gitRunner)
			app.WorkingDir = tempDir
			if tt.mockStorage != nil {
				app.Storage = tt.mockStorage
			}
			app.RootCmd.SetArgs(tt.args)

			err := app.Execute(context.Background())
			if tt.expectedCode == errcode.ExitSuccess {
				require.NoError(t, err)
				if tt.validateOut != nil {
					tt.validateOut(t, stdout.String(), stderr.String())
				}
			} else {
				require.Error(t, err)
				var appErr *errcode.AppError
				require.True(t, errors.As(err, &appErr), "expected *errcode.AppError, got %T: %v", err, err)
				assert.Equal(t, tt.expectedCode, errcode.ExitCode(appErr))
				if tt.errSubstr != "" {
					assert.Contains(t, appErr.Error(), tt.errSubstr)
				}
			}
		})
	}
}

func TestUsageCommand_ProgressSpinnerFormatsShortUUIDWithEllipsis(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	cfg := &domain.ProjectConfig{
		WorkDir: "data",
		Remote: domain.RemoteConfig{
			Host: "backup.example.com",
			Path: "/storage/myrepo",
		},
	}

	gitRunner := &mockGitRunner{
		responses: map[string]mockGitResponse{
			"rev-parse --is-inside-work-tree --show-toplevel --show-prefix": {out: "true\n" + tempDir + "\n"},
			"config --local --get-all remote.origin.fetch":                  {out: git.WildcardFetchRefspec},
		},
	}
	insp := git.NewInspector(gitRunner)
	ops := git.NewOperations(gitRunner)

	stderr := newWatchBuffer()
	logger := term.New(term.Options{
		Stderr:      stderr,
		IsTTY:       true,
		NoColorFlag: true,
	})

	stor := &storage.MockManager{
		QueryUsageFn: func(ctx context.Context, host, basePath string, opts ...storage.QueryUsageOptions) (string, []domain.RemoteRevision, error) {
			require.Len(t, opts, 1)
			require.NotNil(t, opts[0].OnProgress)

			assert.True(t, stderr.WaitFor("Connecting to host 'backup.example.com'", 2*time.Second),
				"expected initial spinner output without trailing ellipsis, got: %s", stderr.String())

			opts[0].OnProgress(0, 18, "")
			assert.True(t, stderr.WaitFor("Calculating total storage usage across 18 revision(s)", 2*time.Second),
				"expected calculating spinner output without trailing ellipsis, got: %s", stderr.String())

			opts[0].OnProgress(0, 0, "")
			assert.True(t, stderr.WaitFor("Calculating total storage usage", 2*time.Second),
				"expected fallback calculating spinner output without trailing ellipsis, got: %s", stderr.String())

			opts[0].OnProgress(1, 18, "df195066-4b75-49ff-bf02-c303eb6c6033")
			assert.True(t, stderr.WaitFor("Scanning revision 1/18 (df195066-4b75...)", 2*time.Second),
				"expected spinner output to contain ellipsized two-segment UUID, got: %s", stderr.String())

			opts[0].OnProgress(2, 18, "short12345678")
			assert.True(t, stderr.WaitFor("Scanning revision 2/18 (short12345678)", 2*time.Second),
				"expected spinner output to keep <=13-char UUID without ellipsis, got: %s", stderr.String())

			return "1.0G", []domain.RemoteRevision{
				{UUID: "df195066-4b75-49ff-bf02-c303eb6c6033", SizeHuman: "1.0G"},
			}, nil
		},
	}

	var stdout bytes.Buffer
	_, err := cli.RunUsage(context.Background(), insp, ops, stor, cfg, logger, &stdout, cli.UsageOptions{
		All:       false,
		CallerDir: tempDir,
	})
	require.NoError(t, err)
	assert.False(t, strings.Contains(stderr.String(), "Connecting to host 'backup.example.com'..."),
		"connecting spinner must not have trailing ellipsis")
	assert.False(t, strings.Contains(stderr.String(), "revision(s)..."),
		"calculating spinner must not have trailing ellipsis")
	assert.False(t, strings.Contains(stderr.String(), "Calculating total storage usage..."),
		"calculating spinner must not have trailing ellipsis")
	assert.False(t, strings.Contains(stderr.String(), "(short12345678...)"),
		"UUID of length <=13 must not have ellipsis appended")
	assert.False(t, strings.Contains(stderr.String(), ")..."),
		"spinner must not stutter with trailing ellipsis after closing parenthesis")
}

func TestRunUsage_BreakdownSorting(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	cfg := &domain.ProjectConfig{
		WorkDir: "data",
		Remote: domain.RemoteConfig{
			Host: "backup.example.com",
			Path: "/storage/myrepo",
		},
	}

	extractBreakdownUUIDs := func(stdout string) []string {
		lines := strings.Split(strings.TrimSpace(stdout), "\n")
		var uuids []string
		inRows := false
		for _, line := range lines {
			if strings.HasPrefix(line, "------------------------------------") {
				inRows = true
				continue
			}
			if !inRows || strings.TrimSpace(line) == "" {
				continue
			}
			fields := strings.Fields(line)
			if len(fields) > 0 {
				uuids = append(uuids, fields[0])
			}
		}
		return uuids
	}

	tests := []struct {
		name            string
		gitLogOut       string
		revisions       []domain.RemoteRevision
		expectedOrder   []string
		expectedCreated map[string]string
	}{
		{
			name: "out-of-order ModTime sorts newest first with interleaved orphaned revisions",
			gitLogOut: "1111111111111111111111111111111111111111\x001111111\x001700000100\x00main\x00" + `{"uuid":"11111111-1111-4111-8111-111111111111","seq":"1"}` + "\x00" +
				"3333333333333333333333333333333333333333\x003333333\x001700000300\x00HEAD -> main\x00" + `{"uuid":"33333333-3333-4333-8333-333333333333","seq":"3"}` + "\x00",
			revisions: []domain.RemoteRevision{
				{UUID: "11111111-1111-4111-8111-111111111111", SizeHuman: "1.0G", ModTime: time.Unix(1700000100, 0)},
				{UUID: "44444444-4444-4444-8444-444444444444", SizeHuman: "500M", ModTime: time.Unix(1700000400, 0)}, // orphaned, newest
				{UUID: "22222222-2222-4222-8222-222222222222", SizeHuman: "750M", ModTime: time.Unix(1700000200, 0)}, // orphaned, middle
				{UUID: "33333333-3333-4333-8333-333333333333", SizeHuman: "1.2G", ModTime: time.Unix(1700000300, 0)},
			},
			expectedOrder: []string{
				"44444444-4444-4444-8444-444444444444",
				"33333333-3333-4333-8333-333333333333",
				"22222222-2222-4222-8222-222222222222",
				"11111111-1111-4111-8111-111111111111",
			},
		},
		{
			name: "equal timestamps tie-break by numeric seq descending then UUID descending",
			gitLogOut: "1111111111111111111111111111111111111111\x001111111\x001700000500\x00main\x00" + `{"uuid":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa","seq":"2"}` + "\x00" +
				"2222222222222222222222222222222222222222\x002222222\x001700000500\x00main\x00" + `{"uuid":"11111111-1111-4111-8111-111111111111","seq":"10"}` + "\x00" +
				"3333333333333333333333333333333333333333\x003333333\x001700000500\x00main\x00" + `{"uuid":"bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb","seq":"10"}` + "\x00" +
				"4444444444444444444444444444444444444444\x004444444\x001700000500\x00main\x00" + `{"uuid":"55555555-5555-4555-8555-555555555555","seq":"alpha"}` + "\x00" +
				"5555555555555555555555555555555555555555\x005555555\x001700000500\x00main\x00" + `{"uuid":"44444444-4444-4444-8444-444444444444","seq":"beta"}` + "\x00",
			revisions: []domain.RemoteRevision{
				{UUID: "cccccccc-cccc-4ccc-8ccc-cccccccccccc", SizeHuman: "100M", ModTime: time.Unix(1700000500, 0)}, // orphaned (no seq)
				{UUID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", SizeHuman: "100M", ModTime: time.Unix(1700000500, 0)}, // seq 2
				{UUID: "11111111-1111-4111-8111-111111111111", SizeHuman: "100M", ModTime: time.Unix(1700000500, 0)}, // seq 10
				{UUID: "dddddddd-dddd-4ddd-8ddd-dddddddddddd", SizeHuman: "100M", ModTime: time.Unix(1700000500, 0)}, // orphaned (no seq)
				{UUID: "55555555-5555-4555-8555-555555555555", SizeHuman: "100M", ModTime: time.Unix(1700000500, 0)}, // seq alpha
				{UUID: "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", SizeHuman: "100M", ModTime: time.Unix(1700000500, 0)}, // seq 10
				{UUID: "44444444-4444-4444-8444-444444444444", SizeHuman: "100M", ModTime: time.Unix(1700000500, 0)}, // seq beta
			},
			expectedOrder: []string{
				"bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", // seq 10, higher UUID
				"11111111-1111-4111-8111-111111111111", // seq 10, lower UUID
				"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", // seq 2 (numeric < 10)
				"44444444-4444-4444-8444-444444444444", // non-numeric seq beta
				"55555555-5555-4555-8555-555555555555", // non-numeric seq alpha
				"dddddddd-dddd-4ddd-8ddd-dddddddddddd", // no seq, higher UUID
				"cccccccc-cccc-4ccc-8ccc-cccccccccccc", // no seq, lower UUID
			},
		},
		{
			name: "zero ModTime falls back to Git commit timestamp and missing timestamps sort last",
			gitLogOut: "1111111111111111111111111111111111111111\x001111111\x001700000250\x00main\x00" + `{"uuid":"22222222-2222-4222-8222-222222222222","seq":"5"}` + "\x00" +
				"2222222222222222222222222222222222222222\x002222222\x000\x00main\x00" + `{"uuid":"44444444-4444-4444-8444-444444444444","seq":"9"}` + "\x00",
			revisions: []domain.RemoteRevision{
				{UUID: "55555555-5555-4555-8555-555555555555", SizeHuman: "100M"},                                    // orphaned, zero ModTime -> last (no seq)
				{UUID: "11111111-1111-4111-8111-111111111111", SizeHuman: "100M", ModTime: time.Unix(1700000100, 0)}, // older ModTime
				{UUID: "22222222-2222-4222-8222-222222222222", SizeHuman: "100M"},                                    // zero ModTime, commit ts 1700000250
				{UUID: "44444444-4444-4444-8444-444444444444", SizeHuman: "100M"},                                    // zero ModTime, commit ts 0 -> untimestamped (with seq 9)
				{UUID: "33333333-3333-4333-8333-333333333333", SizeHuman: "100M", ModTime: time.Unix(1700000300, 0)}, // newest ModTime
			},
			expectedOrder: []string{
				"33333333-3333-4333-8333-333333333333", // ModTime 1700000300
				"22222222-2222-4222-8222-222222222222", // fallback commit ts 1700000250
				"11111111-1111-4111-8111-111111111111", // ModTime 1700000100
				"44444444-4444-4444-8444-444444444444", // untimestamped, has seq 9
				"55555555-5555-4555-8555-555555555555", // untimestamped, orphaned
			},
			expectedCreated: map[string]string{
				"22222222-2222-4222-8222-222222222222": "-",
				"44444444-4444-4444-8444-444444444444": "-",
				"55555555-5555-4555-8555-555555555555": "-",
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			gitRunner := &mockGitRunner{
				responses: map[string]mockGitResponse{
					"rev-parse --is-inside-work-tree --show-toplevel --show-prefix": {out: "true\n" + tempDir + "\n"},
					"config --local --get-all remote.origin.fetch":                  {out: git.WildcardFetchRefspec},
					"log --all -z --format=%H%x00%h%x00%ad%x00%D%x00%s --date=unix": {out: tt.gitLogOut},
				},
			}
			insp := git.NewInspector(gitRunner)
			ops := git.NewOperations(gitRunner)

			stor := &storage.MockManager{
				QueryUsageFn: func(ctx context.Context, host, basePath string, opts ...storage.QueryUsageOptions) (string, []domain.RemoteRevision, error) {
					return "2.0G", tt.revisions, nil
				},
			}

			var stdout bytes.Buffer
			res, err := cli.RunUsage(context.Background(), insp, ops, stor, cfg, nil, &stdout, cli.UsageOptions{
				All:       true,
				CallerDir: tempDir,
			})
			require.NoError(t, err)
			require.Len(t, res.Revisions, len(tt.expectedOrder))

			gotResultOrder := make([]string, len(res.Revisions))
			for i, row := range res.Revisions {
				gotResultOrder[i] = row.UUID
				if wantCreated, ok := tt.expectedCreated[row.UUID]; ok {
					assert.Equal(t, wantCreated, row.Created, "unexpected Created value for %s", row.UUID)
				}
			}
			assert.Equal(t, tt.expectedOrder, gotResultOrder, "UsageResult.Revisions order mismatch")
			assert.Equal(t, tt.expectedOrder, extractBreakdownUUIDs(stdout.String()), "stdout breakdown table order mismatch")
		})
	}
}
