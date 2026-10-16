// Package term provides structured stderr logging, terminal TTY & ANSI color
// policy detection, and wall-clock execution timers for git-drev.
package term

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"git-drev/internal/errcode"
)

// Prefix is the canonical operational log prefix emitted on stderr.
const Prefix = "[git-drev]"

// ANSI SGR color escape sequences.
const (
	ansiReset  = "\x1b[0m"
	ansiRed    = "\x1b[31;1m"
	ansiYellow = "\x1b[33m"
	ansiCyan   = "\x1b[36m"
)

// Options configures a Logger instance.
type Options struct {
	Stderr      io.Writer
	Verbose     bool
	Quiet       bool
	NoColorFlag bool
	EnvLookup   func(key string) (string, bool)
	IsTTY       bool
}

// Logger emits structured operational diagnostics, warnings, hints, and errors
// to stderr.
type Logger struct {
	mu       sync.Mutex
	stderr   io.Writer
	verbose  bool
	quiet    bool
	useColor bool
}

// New constructs a Logger from Options, evaluating color policy and emitting a
// warning to stderr if GIT_DREV_COLOR contains an unrecognized value.
func New(opts Options) *Logger {
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
		stderr:   stderr,
		verbose:  opts.Verbose,
		quiet:    opts.Quiet,
		useColor: useColor,
	}

	if invalidColorVal != "" {
		l.Warnf("Invalid GIT_DREV_COLOR value %q; falling back to \"auto\"", invalidColorVal)
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

	rawColor, hasColorEnv := envLookup("GIT_DREV_COLOR")
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

// Writer returns the underlying stderr io.Writer.
func (l *Logger) Writer() io.Writer {
	return l.stderr
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

// Info emits `[git-drev] <msg>` to stderr unless Quiet is enabled.
func (l *Logger) Info(msg string) {
	if l.quiet {
		return
	}
	l.writeLine(fmt.Sprintf("%s %s", Prefix, msg))
}

// Infof emits a formatted `[git-drev] <msg>` line to stderr unless Quiet is enabled.
func (l *Logger) Infof(format string, args ...any) {
	if l.quiet {
		return
	}
	l.Info(fmt.Sprintf(format, args...))
}

// Debug emits `[git-drev] <msg>` to stderr only when Verbose is enabled and Quiet is false.
func (l *Logger) Debug(msg string) {
	if !l.verbose || l.quiet {
		return
	}
	l.writeLine(fmt.Sprintf("%s %s", Prefix, msg))
}

// Debugf emits a formatted `[git-drev] <msg>` line to stderr only when Verbose is enabled.
func (l *Logger) Debugf(format string, args ...any) {
	if !l.verbose || l.quiet {
		return
	}
	l.Debug(fmt.Sprintf(format, args...))
}

// Warn emits `[git-drev] Warning: <msg>` to stderr.
func (l *Logger) Warn(msg string) {
	if l.useColor {
		l.writeLine(fmt.Sprintf("%s %sWarning: %s%s", Prefix, ansiYellow, msg, ansiReset))
		return
	}
	l.writeLine(fmt.Sprintf("%s Warning: %s", Prefix, msg))
}

// Warnf emits a formatted `[git-drev] Warning: <msg>` line to stderr.
func (l *Logger) Warnf(format string, args ...any) {
	l.Warn(fmt.Sprintf(format, args...))
}

// Hint emits `[git-drev] Hint: <msg>` to stderr unless Quiet is enabled.
func (l *Logger) Hint(msg string) {
	if l.quiet {
		return
	}
	l.HintAlways(msg)
}

// HintAlways emits `[git-drev] Hint: <msg>` to stderr even when Quiet is enabled
// (used for error remediation hints attached to fatal errors).
func (l *Logger) HintAlways(msg string) {
	if l.useColor {
		l.writeLine(fmt.Sprintf("%s %sHint: %s%s", Prefix, ansiCyan, msg, ansiReset))
		return
	}
	l.writeLine(fmt.Sprintf("%s Hint: %s", Prefix, msg))
}

// Error emits `[git-drev] Error: <msg>` to stderr (never suppressed by Quiet).
func (l *Logger) Error(msg string) {
	if l.useColor {
		l.writeLine(fmt.Sprintf("%s %sError: %s%s", Prefix, ansiRed, msg, ansiReset))
		return
	}
	l.writeLine(fmt.Sprintf("%s Error: %s", Prefix, msg))
}

// Errorf emits a formatted `[git-drev] Error: <msg>` line to stderr (never suppressed by Quiet).
func (l *Logger) Errorf(format string, args ...any) {
	l.Error(fmt.Sprintf(format, args...))
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

func (l *Logger) writeLine(line string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	_, _ = fmt.Fprintln(l.stderr, line)
}
