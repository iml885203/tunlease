package cliapp

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/fatih/color"
	"github.com/iml885203/tunlease/pkg/tunnelclient"
	"github.com/mattn/go-colorable"
	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"
)

type console struct {
	out      io.Writer
	err      io.Writer
	colorOut bool
	colorErr bool
	json     bool
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

func newConsoleOutput(out, err io.Writer, output string) *console {
	c := newConsole(out, err)
	c.json = output == "json"
	return c
}

func (c *console) emitJSON(w io.Writer, event map[string]any) {
	event["schema_version"] = 1
	_ = json.NewEncoder(w).Encode(event)
}

func (c *console) event(event map[string]any) {
	c.emitJSON(c.out, event)
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
	if c.json {
		c.emitJSON(c.out, map[string]any{"type": "message", "level": "success", "message": message})
		return
	}
	_, _ = fmt.Fprintln(c.out, styled(c.colorOut, message, color.FgGreen))
}

func (c *console) info(format string, args ...any) {
	if c.json {
		c.emitJSON(c.out, map[string]any{"type": "message", "level": "info", "message": fmt.Sprintf(format, args...)})
		return
	}
	_, _ = fmt.Fprintf(c.out, format+"\n", args...)
}

func (c *console) status(format string, args ...any) {
	message := fmt.Sprintf(format, args...)
	if c.json {
		c.emitJSON(c.out, map[string]any{"type": "message", "level": "status", "message": message})
		return
	}
	_, _ = fmt.Fprintln(c.out, styled(c.colorOut, message, color.FgCyan))
}

func (c *console) noticeOut(format string, args ...any) {
	message := fmt.Sprintf(format, args...)
	if c.json {
		c.emitJSON(c.out, map[string]any{"type": "message", "level": "info", "message": message})
		return
	}
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
	if c.json {
		c.emitJSON(w, map[string]any{"type": "warning", "code": "warning", "message": message})
		return
	}
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
	if c.json {
		c.event(map[string]any{
			"type": "request", "method": method, "path": path,
			"status": status, "duration": duration,
		})
		return
	}
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

func (c *console) claimHeader() {
	if c.json {
		return
	}
	_, _ = fmt.Fprintf(c.out, "%-40s %-24s %-16s %s\n", "PATH", "FORWARDS TO / OWNER", "STATUS", "STARTED")
}

// PrintError colorizes a top-level CLI failure and adds an actionable hint for
// structured gateway errors. Generic errors keep their original text.
func PrintError(w io.Writer, err error) {
	newConsole(io.Discard, w).failure("Error: %s", actionableError(err))
}

// PrintCommandError emits the opt-in JSON error contract. Raw arguments are
// inspected because flag parsing may stop at an earlier unknown flag.
func PrintCommandError(w io.Writer, command *cobra.Command, args []string, err error) {
	output := RequestedOutput(args)
	if output == "" && command != nil {
		if flag := command.Flags().Lookup("output"); flag != nil {
			output = flag.Value.String()
		}
	}
	if output == "json" {
		code, message, action := errorDetails(err)
		event := map[string]any{
			"schema_version": 1,
			"type":           "error",
			"code":           code,
			"message":        message,
		}
		if action != "" {
			event["action"] = action
		}
		var fields interface{ CLIErrorFields() map[string]any }
		if errors.As(err, &fields) {
			for key, value := range fields.CLIErrorFields() {
				event[key] = value
			}
		}
		_ = json.NewEncoder(w).Encode(event)
		return
	}
	PrintError(w, err)
}

// RequestedOutput finds the last requested output mode without depending on
// successful flag parsing. It intentionally stops at the "--" separator.
func RequestedOutput(args []string) string {
	output := ""
	for index := 0; index < len(args); index++ {
		arg := args[index]
		if arg == "--" {
			break
		}
		switch {
		case arg == "--output" || arg == "-o":
			if index+1 < len(args) {
				index++
				output = args[index]
			}
		case strings.HasPrefix(arg, "--output="):
			output = strings.TrimPrefix(arg, "--output=")
		case strings.HasPrefix(arg, "-o="):
			output = strings.TrimPrefix(arg, "-o=")
		case strings.HasPrefix(arg, "-") && !strings.HasPrefix(arg, "--"):
			body := strings.TrimPrefix(arg, "-")
			if outputIndex := strings.IndexByte(body, 'o'); outputIndex >= 0 &&
				strings.Trim(body[:outputIndex], "dka") == "" {
				value := strings.TrimPrefix(body[outputIndex+1:], "=")
				if value != "" {
					output = value
				}
			}
		}
	}
	return output
}

func actionableError(err error) string {
	code, message, action := errorDetails(err)
	if code == "command_failed" {
		return message
	}
	result := fmt.Sprintf("%s — %s", code, message)
	if action != "" {
		result += ". " + action
	}
	return result
}

type cliErrorCoder interface {
	CLIErrorCode() string
}

func errorDetails(err error) (code, message, action string) {
	var apiErr *tunnelclient.APIError
	if errors.As(err, &apiErr) && apiErr.Code != "" {
		code = apiErr.Code
	} else {
		var coded cliErrorCoder
		if errors.As(err, &coded) {
			code = coded.CLIErrorCode()
		}
	}
	if code == "" {
		code = "command_failed"
	}
	action = map[string]string{
		"unauthorized":              "Check --token or TUNLEASE_TOKEN.",
		"invalid_request":           "Check the path and command arguments.",
		"path_claimed":              "Run tul list --all or choose a non-overlapping path.",
		"path_not_allowed":          "Ask the gateway operator for an allowed path.",
		"claim_limit_reached":       "Ask the gateway operator to free capacity.",
		"owner_claim_limit_reached": "Release one of your paths and retry.",
		"claim_expired":             "Run tul claim again.",
		"partial_release":           "Retry the same release command for the remaining paths.",
	}[code]
	return code, err.Error(), action
}
