package term

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
)

// SetStdin overrides standard input reader for prompts (e.g. for testing or automation).
func (l *Logger) SetStdin(stdin io.Reader) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.stdin = stdin
}

// Confirm prompts the user for interactive confirmation [y/n] with up to 5 attempts.
func (l *Logger) Confirm(prompt string) (bool, error) {
	l.mu.Lock()
	r := l.stdin
	if r == nil {
		r = os.Stdin
	}
	l.mu.Unlock()

	reader := bufio.NewReader(r)
	for count := 1; count <= 5; count++ {
		l.mu.Lock()
		fmt.Fprintf(l.stderr, "%s %s: ", Prefix, prompt)
		l.mu.Unlock()

		line, err := reader.ReadString('\n')
		if err != nil && line == "" {
			return false, err
		}
		trimmed := strings.TrimSpace(line)
		switch strings.ToLower(trimmed) {
		case "":
			if strings.Contains(prompt, "[Y/n]") {
				return true, nil
			}
			if strings.Contains(prompt, "[y/N]") {
				return false, nil
			}
			l.Info("Please enter 'y' or 'n'")
		case "y", "yes":
			return true, nil
		case "n", "no":
			return false, nil
		default:
			l.Info("Unrecognized response; please enter 'y' or 'n'")
		}
		if count == 5 {
			l.Info("Too many invalid attempts; aborting")
			return false, nil
		}
	}
	return false, nil
}
