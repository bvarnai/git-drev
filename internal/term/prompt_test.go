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
