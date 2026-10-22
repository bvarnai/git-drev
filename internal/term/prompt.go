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
	l.stdinReader = nil
}

func (l *Logger) getStdinReader() *bufio.Reader {
	if l.stdinReader == nil {
		r := l.stdin
		if r == nil {
			r = os.Stdin
		}
		l.stdinReader = bufio.NewReader(r)
	}
	return l.stdinReader
}

// Confirm prompts the user for interactive confirmation [y/n] with up to 5 attempts.
func (l *Logger) Confirm(prompt string) (bool, error) {
	l.mu.Lock()
	reader := l.getStdinReader()
	l.mu.Unlock()

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

// Prompt prompts the user for a text input with an optional default value.
// If the user inputs an empty line and defaultVal is provided, defaultVal is returned.
// If both input and defaultVal are empty, it re-prompts up to 5 attempts.
func (l *Logger) Prompt(label, defaultVal string) (string, error) {
	l.mu.Lock()
	reader := l.getStdinReader()
	l.mu.Unlock()

	for count := 1; count <= 5; count++ {
		l.mu.Lock()
		if defaultVal != "" {
			fmt.Fprintf(l.stderr, "%s %s [%s]: ", Prefix, label, defaultVal)
		} else {
			fmt.Fprintf(l.stderr, "%s %s: ", Prefix, label)
		}
		l.mu.Unlock()

		line, err := reader.ReadString('\n')
		if err != nil && line == "" {
			return "", err
		}
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			return trimmed, nil
		}
		if defaultVal != "" {
			return defaultVal, nil
		}
		l.Info("A non-empty value is required")
		if count == 5 {
			l.Info("Too many empty attempts; aborting")
			return "", fmt.Errorf("prompt aborted after too many empty attempts")
		}
	}
	return defaultVal, nil
}
