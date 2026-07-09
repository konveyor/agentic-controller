package logging

import (
	"fmt"
	"os"

	"golang.org/x/term"
)

var colorEnabled = term.IsTerminal(int(os.Stderr.Fd()))

const (
	reset  = "\033[0m"
	red    = "\033[0;31m"
	green  = "\033[0;32m"
	yellow = "\033[0;33m"
	cyan   = "\033[0;36m"
	bold   = "\033[1m"
	blue   = "\033[0;34m"
)

func color(c, msg string) string {
	if !colorEnabled {
		return msg
	}
	return c + msg + reset
}

func Info(format string, a ...any) {
	fmt.Fprintf(os.Stderr, color(cyan, "ℹ ")+format+"\n", a...)
}

func Ok(format string, a ...any) {
	fmt.Fprintf(os.Stderr, color(green, "✓ ")+format+"\n", a...)
}

func Warn(format string, a ...any) {
	fmt.Fprintf(os.Stderr, color(yellow, "⚠ ")+format+"\n", a...)
}

func Err(format string, a ...any) {
	fmt.Fprintf(os.Stderr, color(red, "✗ ")+format+"\n", a...)
}

func Fatal(format string, a ...any) {
	Err(format, a...)
	os.Exit(1)
}

func Header(format string, a ...any) {
	msg := fmt.Sprintf(format, a...)
	fmt.Fprintf(os.Stderr, "\n%s\n", color(bold+blue, msg))
}
