package cliapp

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/fatih/color"
	"github.com/mattn/go-colorable"
	"github.com/mattn/go-isatty"
)

type console struct {
	out      io.Writer
	err      io.Writer
	colorOut bool
	colorErr bool
}

func newConsole(out, err io.Writer) *console {
	colorOut := supportsColor(out)
	colorErr := supportsColor(err)
	return &console{
		out:      colorWriter(out, colorOut),
		err:      colorWriter(err, colorErr),
		colorOut: colorOut,
		colorErr: colorErr,
	}
}

func colorWriter(w io.Writer, enabled bool) io.Writer {
	if !enabled {
		return w
	}
	if f, ok := w.(*os.File); ok {
		return colorable.NewColorable(f)
	}
	return w
}

func supportsColor(w io.Writer) bool {
	if !colorPermitted() {
		return false
	}
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	fd := f.Fd()
	return isatty.IsTerminal(fd) || isatty.IsCygwinTerminal(fd)
}

func colorPermitted() bool {
	if os.Getenv("NO_COLOR") != "" || strings.EqualFold(os.Getenv("TERM"), "dumb") {
		return false
	}
	return true
}

func styled(enabled bool, text string, attributes ...color.Attribute) string {
	style := color.New(attributes...)
	if enabled {
		style.EnableColor()
	} else {
		style.DisableColor()
	}
	return style.Sprint(text)
}

func (c *console) success(format string, args ...any) {
	message := fmt.Sprintf(format, args...)
	_, _ = fmt.Fprintln(c.out, styled(c.colorOut, message, color.FgGreen))
}

func (c *console) info(format string, args ...any) {
	_, _ = fmt.Fprintf(c.out, format+"\n", args...)
}

func (c *console) status(format string, args ...any) {
	message := fmt.Sprintf(format, args...)
	_, _ = fmt.Fprintln(c.out, styled(c.colorOut, message, color.FgCyan))
}

func (c *console) noticeOut(format string, args ...any) {
	message := fmt.Sprintf(format, args...)
	_, _ = fmt.Fprintln(c.out, styled(c.colorOut, message, color.FgCyan))
}

func (c *console) warning(format string, args ...any) {
	c.warningTo(c.err, c.colorErr, format, args...)
}

func (c *console) warningOut(format string, args ...any) {
	c.warningTo(c.out, c.colorOut, format, args...)
}

func (c *console) warningTo(w io.Writer, enabled bool, format string, args ...any) {
	message := fmt.Sprintf(format, args...)
	if !enabled {
		_, _ = fmt.Fprintln(w, message)
		return
	}
	const prefix = "WARNING:"
	if strings.HasPrefix(message, prefix) {
		prefixText := styled(true, prefix, color.FgYellow, color.Bold)
		body := styled(true, strings.TrimPrefix(message, prefix), color.FgYellow)
		_, _ = fmt.Fprintln(w, prefixText+body)
		return
	}
	_, _ = fmt.Fprintln(w, styled(true, message, color.FgYellow))
}

func (c *console) failure(format string, args ...any) {
	message := styled(c.colorErr, fmt.Sprintf(format, args...), color.FgRed)
	_, _ = fmt.Fprintln(c.err, message)
}

func (c *console) activity(method, path string, status int, duration string) {
	statusColor := color.FgGreen
	switch {
	case status >= 500:
		statusColor = color.FgRed
	case status >= 400:
		statusColor = color.FgYellow
	case status >= 300:
		statusColor = color.FgCyan
	}
	prefix := styled(c.colorOut, "→", statusColor, color.Bold)
	method = styled(c.colorOut, method, color.Bold)
	statusText := styled(c.colorOut, fmt.Sprint(status), statusColor, color.Bold)
	duration = styled(c.colorOut, duration, color.Faint)
	_, _ = fmt.Fprintf(c.out, "%s %s %s  %s  %s\n", prefix, method, path, statusText, duration)
}

func (c *console) claimRow(paths, target, status, started, suffix string, own, expiring bool) {
	paths = fmt.Sprintf("%-40s", paths)
	target = fmt.Sprintf("%-24s", target)
	status = fmt.Sprintf("%-16s", status)
	if own {
		target = styled(c.colorOut, target, color.FgCyan)
		suffix = styled(c.colorOut, suffix, color.FgGreen)
	}
	if expiring {
		status = styled(c.colorOut, status, color.FgCyan)
	} else {
		status = styled(c.colorOut, status, color.FgGreen)
	}
	_, _ = fmt.Fprintf(c.out, "%s %s %s %s%s\n", paths, target, status, started, suffix)
}

// PrintError colorizes a top-level CLI failure without changing its text.
func PrintError(w io.Writer, err error) {
	newConsole(io.Discard, w).failure("Error: %v", err)
}
