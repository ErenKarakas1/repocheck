package health

import (
	"bufio"
	"fmt"
	"io"
	"slices"
	"strings"
)

type ReportOptions struct {
	Verbose bool // show full paths and every check, not just the ones with problems
	Color   bool // color statuses with ANSI escape codes
}

var statusColors = [...]string{
	Passed:  "32",   // green
	Warning: "33",   // yellow
	Failed:  "31",   // red
	Errored: "1;31", // bold red
}

func WriteReport(w io.Writer, repos []RepoResult, opts ReportOptions) error {
	bw := bufio.NewWriter(w)
	paint := func(s string, status Status) string {
		if !opts.Color {
			return s
		}
		return "\x1b[" + statusColors[status] + "m" + s + "\x1b[0m"
	}
	isWarning := func(c CheckResult) bool { return c.Status == Warning }
	var withProblems, withErrors, withWarnings int

	for _, repo := range repos {
		status := repo.Status()
		switch status {
		case Failed:
			withProblems++
		case Errored:
			withErrors++
		}
		if slices.ContainsFunc(repo.Checks, isWarning) {
			withWarnings++
		}

		name := repo.Name
		if opts.Verbose {
			name = repo.Path
		}

		switch {
		case repo.Err != nil:
			fmt.Fprintf(bw, "%s: %s\n    %s\n", name, paint("ERROR", Errored), indent(repo.Err.Error()))
		case status == Passed && !opts.Verbose:
			fmt.Fprintf(bw, "%s: %s\n", name, paint("OK", Passed))
		default:
			fmt.Fprintf(bw, "%s:\n", name)
			for _, c := range repo.Checks {
				if c.Status == Passed && !opts.Verbose {
					continue
				}
				fmt.Fprintf(bw, "  %s  %s", paint(fmt.Sprintf("%-6s", c.Status), c.Status), c.Check)
				if c.Detail != "" {
					fmt.Fprintf(bw, ": %s", indent(c.Detail)) // fetch errors can be multi-line
				}
				fmt.Fprintln(bw)
			}
		}
		fmt.Fprintln(bw)
	}

	fmt.Fprintf(bw, "%s, %d with problems, %s",
		pluralize(len(repos), "repo", "repos"), withProblems, pluralize(withErrors, "error", "errors"))
	if withWarnings > 0 {
		fmt.Fprintf(bw, ", %s", pluralize(withWarnings, "warning", "warnings"))
	}
	fmt.Fprintln(bw)
	return bw.Flush()
}

func indent(s string) string {
	return strings.ReplaceAll(s, "\n", "\n    ")
}
