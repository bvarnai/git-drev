// Package term provides structured stderr logging, terminal TTY & ANSI color
// policy detection, and wall-clock execution timers for git-drev.
package term

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"github.com/bvarnai/git-drev/internal/errcode"
)

// Prefix is the canonical operational log prefix emitted on stderr.
const Prefix = "[git-drev]"

// ColorEnvVar is the environment variable controlling ANSI color output.
const ColorEnvVar = "GIT_DREV_COLOR"

// ANSI SGR color escape sequences.
const (
	ansiReset   = "\x1b[0m"
	ansiRed     = "\x1b[31;1m"
	ansiYellow  = "\x1b[33m"
	ansiCyan    = "\x1b[36m"
	ansiMagenta = "\x1b[35m"
)

// Options configures a Logger instance.
type Options struct {
	Stdin       io.Reader
	Stdout      io.Writer
	Stderr      io.Writer
	Verbose     bool
	Quiet       bool
	NoColorFlag bool
	EnvLookup   func(key string) (string, bool)
	IsTTY       bool
}

// Logger emits structured operational diagnostics, warnings, hints, and errors
// to stderr, and provides stream and terminal formatting utilities.
type Logger struct {
	mu        sync.Mutex
	stdin     io.Reader
	stdout    io.Writer
	stderr    io.Writer
	verbose   bool
	quiet     bool
	useColor  bool
	isTTY     bool
	envLookup func(key string) (string, bool)
}

// New constructs a Logger from Options, evaluating color policy and emitting a
// warning to stderr if GIT_DREV_COLOR contains an unrecognized value.
func New(opts Options) *Logger {
	stdin := opts.Stdin
	if stdin == nil {
		stdin = os.Stdin
	}
	stdout := opts.Stdout
	if stdout == nil {
		stdout = os.Stdout
	}
	stderr := opts.Stderr
	if stderr == nil {
		stderr = os.Stderr
	}
	envLookup := opts.EnvLookup
	if envLookup == nil {
		envLookup = os.LookupEnv
	}

	useColor, invalidColorVal := ResolveColorPolicy(opts.NoColorFlag, opts.IsTTY, envLookup)

	l := &Logger{
		stdin:     stdin,
		stdout:    stdout,
		stderr:    stderr,
		verbose:   opts.Verbose,
		quiet:     opts.Quiet,
		useColor:  useColor,
		isTTY:     opts.IsTTY,
		envLookup: envLookup,
	}

	if invalidColorVal != "" {
		l.Warnf("Invalid %s value %q; falling back to \"auto\"", ColorEnvVar, invalidColorVal)
	}
	return l
}

// IsStreamTTY reports whether w is backed by an interactive character device (*os.File).
func IsStreamTTY(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok || f == nil {
		return false
	}
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}

// ResolveColorPolicy determines whether ANSI colors should be emitted for a stream
// according to docs/spec/global.md Section 1 & Section 2:
//  1. If --no-color flag is passed, color is disabled.
//  2. GIT_DREV_COLOR is evaluated case-insensitively:
//     - "always", "true", "1": forces color on (even on non-TTY or when NO_COLOR is set, unless --no-color flag is set).
//     - "never", "false", "0": forces color off.
//     - "auto" or "": continues to NO_COLOR / TERM / TTY checks.
//     - Any other value: returns invalidColorVal so caller can warn, and falls back to "auto".
//  3. If NO_COLOR is present and non-empty, or TERM == "dumb", color is disabled.
//  4. Otherwise, color is enabled if and only if isTTY is true.
func ResolveColorPolicy(noColorFlag bool, isTTY bool, envLookup func(string) (string, bool)) (useColor bool, invalidColorVal string) {
	if envLookup == nil {
		envLookup = os.LookupEnv
	}

	rawColor, hasColorEnv := envLookup(ColorEnvVar)
	trimmedColor := strings.TrimSpace(rawColor)
	lowerColor := strings.ToLower(trimmedColor)

	mode := "auto"
	if hasColorEnv && trimmedColor != "" {
		switch lowerColor {
		case "auto":
			mode = "auto"
		case "always", "true", "1":
			mode = "always"
		case "never", "false", "0":
			mode = "never"
		default:
			invalidColorVal = rawColor
			mode = "auto"
		}
	}

	if noColorFlag {
		return false, invalidColorVal
	}
	if mode == "never" {
		return false, invalidColorVal
	}
	if mode == "always" {
		return true, invalidColorVal
	}

	if noColor, ok := envLookup("NO_COLOR"); ok && noColor != "" {
		return false, invalidColorVal
	}
	if term, ok := envLookup("TERM"); ok && strings.EqualFold(strings.TrimSpace(term), "dumb") {
		return false, invalidColorVal
	}

	return isTTY, invalidColorVal
}

// Stderr returns the underlying stderr io.Writer.
func (l *Logger) Stderr() io.Writer {
	return l.stderr
}

// Writer returns the underlying stderr io.Writer.
func (l *Logger) Writer() io.Writer {
	return l.stderr
}

// Stdout returns the underlying stdout io.Writer.
func (l *Logger) Stdout() io.Writer {
	return l.stdout
}

// Verbose reports whether verbose logging is enabled.
func (l *Logger) Verbose() bool {
	return l.verbose
}

// Quiet reports whether quiet mode is enabled.
func (l *Logger) Quiet() bool {
	return l.quiet
}

// UseColor reports whether ANSI escape sequences are enabled on this logger.
func (l *Logger) UseColor() bool {
	return l.useColor
}

// IsTTY reports whether the underlying stderr stream is an interactive terminal.
func (l *Logger) IsTTY() bool {
	return l.isTTY
}

// Out writes functional data to stdout followed by a newline.
func (l *Logger) Out(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(args) == 0 {
		_, _ = fmt.Fprintln(l.stdout, format)
	} else {
		_, _ = fmt.Fprintf(l.stdout, format+"\n", args...)
	}
}

// Info emits `[git-drev] <msg>` to stderr unless Quiet is enabled.
func (l *Logger) Info(format string, args ...any) {
	if l.quiet {
		return
	}
	msg := format
	if len(args) > 0 {
		msg = fmt.Sprintf(format, args...)
	}
	l.writeLine(fmt.Sprintf("%s %s", Prefix, msg))
}

// Infof emits a formatted `[git-drev] <msg>` line to stderr unless Quiet is enabled.
func (l *Logger) Infof(format string, args ...any) {
	l.Info(format, args...)
}

// Debug emits `[git-drev] Debug: <msg>` to stderr if verbose mode is enabled.
func (l *Logger) Debug(format string, args ...any) {
	if !l.verbose {
		return
	}
	msg := format
	if len(args) > 0 {
		msg = fmt.Sprintf(format, args...)
	}
	if l.useColor {
		l.writeLine(fmt.Sprintf("%s %sDebug: %s%s", Prefix, ansiMagenta, msg, ansiReset))
		return
	}
	l.writeLine(fmt.Sprintf("%s Debug: %s", Prefix, msg))
}

// Debugf emits a formatted `[git-drev] Debug: <msg>` line to stderr if verbose mode is enabled.
func (l *Logger) Debugf(format string, args ...any) {
	l.Debug(format, args...)
}

// Warn emits `[git-drev] Warning: <msg>` to stderr.
func (l *Logger) Warn(format string, args ...any) {
	msg := format
	if len(args) > 0 {
		msg = fmt.Sprintf(format, args...)
	}
	if l.useColor {
		l.writeLine(fmt.Sprintf("%s %sWarning: %s%s", Prefix, ansiYellow, msg, ansiReset))
		return
	}
	l.writeLine(fmt.Sprintf("%s Warning: %s", Prefix, msg))
}

// Warnf emits a formatted `[git-drev] Warning: <msg>` line to stderr.
func (l *Logger) Warnf(format string, args ...any) {
	l.Warn(format, args...)
}

// Hint emits `[git-drev] Hint: <msg>` to stderr unless Quiet is enabled.
func (l *Logger) Hint(format string, args ...any) {
	if l.quiet {
		return
	}
	l.HintAlways(format, args...)
}

// HintAlways emits `[git-drev] Hint: <msg>` to stderr even when Quiet is enabled
// (used for error remediation hints attached to fatal errors).
func (l *Logger) HintAlways(format string, args ...any) {
	msg := format
	if len(args) > 0 {
		msg = fmt.Sprintf(format, args...)
	}
	if l.useColor {
		l.writeLine(fmt.Sprintf("%s %sHint: %s%s", Prefix, ansiCyan, msg, ansiReset))
		return
	}
	l.writeLine(fmt.Sprintf("%s Hint: %s", Prefix, msg))
}

// Error emits `[git-drev] Error: <msg>` to stderr (never suppressed by Quiet).
func (l *Logger) Error(format string, args ...any) {
	msg := format
	if len(args) > 0 {
		msg = fmt.Sprintf(format, args...)
	}
	if l.useColor {
		l.writeLine(fmt.Sprintf("%s %sError: %s%s", Prefix, ansiRed, msg, ansiReset))
		return
	}
	l.writeLine(fmt.Sprintf("%s Error: %s", Prefix, msg))
}

// Errorf emits a formatted `[git-drev] Error: <msg>` line to stderr (never suppressed by Quiet).
func (l *Logger) Errorf(format string, args ...any) {
	l.Error(format, args...)
}

// LogError emits `[git-drev] Error: <err>` and, if err carries an errcode.Hint, follows it
// with `[git-drev] Hint: <remediation>` on stderr.
func (l *Logger) LogError(err error) {
	if err == nil {
		return
	}
	l.Error(err.Error())
	if hint := errcode.Hint(err); hint != "" {
		l.HintAlways(hint)
	}
}

// Hyperlink formats a URL as an OSC 8 clickable terminal hyperlink if color is enabled.
// If text is empty, the URL itself is used as the link text.
func (l *Logger) Hyperlink(url, text string) string {
	if text == "" {
		text = url
	}
	if !l.useColor || url == "" {
		return text
	}
	// OSC 8 hyperlink escape sequence: \033]8;;URL\033\TEXT\033]8;;\033\
	return fmt.Sprintf("\033]8;;%s\033\\%s\033]8;;\033\\", url, text)
}

// Width returns the detected terminal width in columns, defaulting to 80 if undetermined.
func (l *Logger) Width() int {
	lookup := l.envLookup
	if lookup == nil {
		lookup = os.LookupEnv
	}
	if cols, ok := lookup("COLUMNS"); ok && cols != "" {
		var c int
		if _, err := fmt.Sscanf(cols, "%d", &c); err == nil && c > 20 {
			return c
		}
	}
	return 80
}

// WrapText word-wraps text with hanging indentation to fit within maxCols.
// prefix is prepended to the first line.
// indent is prepended to all subsequent lines.
func (l *Logger) WrapText(prefix, indent, text string, maxCols int) []string {
	if maxCols <= 0 {
		maxCols = l.Width()
	}

	words := strings.Fields(text)
	if len(words) == 0 {
		return []string{prefix}
	}

	var lines []string
	current := prefix
	currMax := maxCols

	for _, word := range words {
		spaceNeeded := 0
		if current != prefix && current != indent {
			spaceNeeded = 1
		}

		if len(current)+spaceNeeded+len(word) > currMax && (current != prefix && current != indent) {
			lines = append(lines, current)
			current = indent + word
		} else {
			if spaceNeeded > 0 {
				current += " " + word
			} else {
				current += word
			}
		}
	}

	if current != "" {
		lines = append(lines, current)
	}

	return lines
}

func (l *Logger) writeLine(line string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	_, _ = fmt.Fprintln(l.stderr, line)
}
