package term_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/bvarnai/git-drev/internal/term"
)

func TestLogger_Confirm(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		input      string
		prompt     string
		wantResult bool
		wantErr    bool
	}{
		{
			name:       "yes lowercase",
			input:      "y\n",
			prompt:     "Proceed [Y/n]?",
			wantResult: true,
		},
		{
			name:       "yes uppercase",
			input:      "YES\n",
			prompt:     "Proceed [Y/n]?",
			wantResult: true,
		},
		{
			name:       "no lowercase",
			input:      "n\n",
			prompt:     "Proceed [Y/n]?",
			wantResult: false,
		},
		{
			name:       "no uppercase",
			input:      "NO\n",
			prompt:     "Proceed [Y/n]?",
			wantResult: false,
		},
		{
			name:       "default yes on empty input with [Y/n]",
			input:      "\n",
			prompt:     "Proceed [Y/n]?",
			wantResult: true,
		},
		{
			name:       "default no on empty input with [y/N]",
			input:      "\n",
			prompt:     "Proceed [y/N]?",
			wantResult: false,
		},
		{
			name:       "retry after invalid input then yes",
			input:      "invalid\ny\n",
			prompt:     "Proceed [Y/n]?",
			wantResult: true,
		},
		{
			name:       "retry after invalid input then no",
			input:      "what?\nno\n",
			prompt:     "Proceed [Y/n]?",
			wantResult: false,
		},
		{
			name:       "EOF returns error",
			input:      "",
			prompt:     "Proceed [Y/n]?",
			wantResult: false,
			wantErr:    true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var stderr bytes.Buffer
			logger := term.New(term.Options{
				Stderr: &stderr,
			})
			logger.SetStdin(strings.NewReader(tc.input))

			got, err := logger.Confirm(tc.prompt)
			if (err != nil) != tc.wantErr {
				t.Fatalf("Confirm() error = %v, wantErr %v", err, tc.wantErr)
			}
			if got != tc.wantResult {
				t.Fatalf("Confirm() = %v, wantResult %v", got, tc.wantResult)
			}
		})
	}
}

func TestLogger_Prompt(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		input      string
		label      string
		defaultVal string
		wantResult string
		wantErr    bool
	}{
		{
			name:       "custom text entered",
			input:      "storage.example.com\n",
			label:      "Remote host",
			defaultVal: "",
			wantResult: "storage.example.com",
		},
		{
			name:       "empty input accepts default",
			input:      "\n",
			label:      "Working directory",
			defaultVal: "data",
			wantResult: "data",
		},
		{
			name:       "whitespace trimmed and overrides default",
			input:      "  custom-dir  \n",
			label:      "Working directory",
			defaultVal: "data",
			wantResult: "custom-dir",
		},
		{
			name:       "retry after empty input when required",
			input:      "\n\nmy-host\n",
			label:      "Remote host",
			defaultVal: "",
			wantResult: "my-host",
		},
		{
			name:       "exceeds retries when required",
			input:      "\n\n\n\n\n\n",
			label:      "Remote host",
			defaultVal: "",
			wantErr:    true,
		},
		{
			name:       "EOF returns error",
			input:      "",
			label:      "Remote host",
			defaultVal: "",
			wantErr:    true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var stderr bytes.Buffer
			logger := term.New(term.Options{
				Stderr: &stderr,
			})
			logger.SetStdin(strings.NewReader(tc.input))

			got, err := logger.Prompt(tc.label, tc.defaultVal)
			if (err != nil) != tc.wantErr {
				t.Fatalf("Prompt() error = %v, wantErr %v", err, tc.wantErr)
			}
			if got != tc.wantResult {
				t.Fatalf("Prompt() = %q, wantResult %q", got, tc.wantResult)
			}
		})
	}
}

func TestLogger_SequentialPrompts(t *testing.T) {
	t.Parallel()

	// Simulate interactive sequence: Prompt host -> Prompt path -> Prompt workdir -> Confirm commit
	input := "remote.server\n/srv/drev/repo\n\ny\n"
	var stderr bytes.Buffer
	logger := term.New(term.Options{
		Stderr: &stderr,
	})
	logger.SetStdin(strings.NewReader(input))

	host, err := logger.Prompt("Remote host", "")
	if err != nil || host != "remote.server" {
		t.Fatalf("Prompt host = %q, err = %v", host, err)
	}

	path, err := logger.Prompt("Remote path", "")
	if err != nil || path != "/srv/drev/repo" {
		t.Fatalf("Prompt path = %q, err = %v", path, err)
	}

	workdir, err := logger.Prompt("Workdir", "data")
	if err != nil || workdir != "data" {
		t.Fatalf("Prompt workdir = %q, err = %v", workdir, err)
	}

	confirmed, err := logger.Confirm("Commit changes [Y/n]?")
	if err != nil || !confirmed {
		t.Fatalf("Confirm = %v, err = %v", confirmed, err)
	}
}
