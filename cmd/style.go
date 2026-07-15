package cmd

import (
	"os"

	"github.com/mattn/go-isatty"
)

// styler wraps text in ANSI color codes, but only when writing to a
// terminal. On a pipe or file it returns strings unchanged so captured
// output stays clean.
type styler struct{ color bool }

// newStyler enables color only when f is a terminal.
func newStyler(f *os.File) styler {
	return styler{color: isatty.IsTerminal(f.Fd())}
}

func (s styler) wrap(code, str string) string {
	if !s.color {
		return str
	}
	return "\x1b[" + code + "m" + str + "\x1b[0m"
}

func (s styler) green(str string) string { return s.wrap("32", str) }
func (s styler) red(str string) string   { return s.wrap("31", str) }
func (s styler) dim(str string) string   { return s.wrap("2", str) }
func (s styler) bold(str string) string  { return s.wrap("1", str) }
