package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"

	"github.com/ErenKarakas1/repocheck/src/health"
)

const (
	exitPassed = 0 // every check passed
	exitFailed = 1 // a check failed
	exitError  = 2 // a check or the program itself ran into an error
)

const usage = `A utility to check the health of the git repositories in a directory.

Usage: repocheck [OPTIONS] [PATH]

PATH is the directory whose subdirectories are checked (default: .)

Options:
  -v, --verbose                Show every check, not just the ones with problems
      --follow-symlinks        Include symlinked repositories
      --fetch                  Run 'git fetch --all --prune' first, so ahead/behind counts are current
      --pull                   Fetch, then fast-forward the checked-out branch if the working tree is clean
      --branch-report-limit N  Max number of branches listed per check (default: 10)
  -j, --jobs N                 Number of repositories to check in parallel, 0 = one per CPU (default: 0)
  -h, --help                   Show this help message

Exits with 0 if every check passed, 1 if any check failed, or 2 on errors. A failed fetch is only a warning.
`

func main() {
	log.SetFlags(0)
	log.SetPrefix("repocheck: ")
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	cfg, verbose, err := parseArgs(args)
	if errors.Is(err, flag.ErrHelp) {
		fmt.Print(usage)
		return exitPassed
	}
	if err != nil {
		log.Printf("%v\nRun 'repocheck -h' for usage.", err)
		return exitError
	}

	if cfg.Path == "" {
		if cfg.Path, err = os.Getwd(); err != nil {
			log.Print(err)
			return exitError
		}
	}

	repos, err := health.CheckRepos(cfg)
	if err != nil {
		log.Print(err)
		return exitError
	}
	opts := health.ReportOptions{
		Verbose: verbose,
		Color:   os.Getenv("NO_COLOR") == "" && isTerminal(os.Stdout), // https://no-color.org
	}
	if err := health.WriteReport(os.Stdout, repos, opts); err != nil {
		log.Print(err)
		return exitError
	}

	worst := health.Passed
	for _, repo := range repos {
		worst = max(worst, repo.Status())
	}
	switch worst {
	case health.Failed:
		return exitFailed
	case health.Errored:
		return exitError
	default: // warnings don't fail the run
		return exitPassed
	}
}

// close enough...
func isTerminal(f *os.File) bool {
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func parseArgs(args []string) (cfg health.Config, verbose bool, err error) {
	fs := flag.NewFlagSet("repocheck", flag.ContinueOnError)
	fs.SetOutput(io.Discard) // run() prints errors and usage itself

	fs.BoolVar(&verbose, "v", false, "")
	fs.BoolVar(&verbose, "verbose", false, "")
	fs.BoolVar(&cfg.FollowSymlinks, "follow-symlinks", false, "")
	fs.BoolVar(&cfg.Fetch, "fetch", false, "")
	fs.BoolVar(&cfg.Pull, "pull", false, "")
	fs.IntVar(&cfg.BranchReportLimit, "branch-report-limit", 10, "")
	fs.IntVar(&cfg.Jobs, "j", 0, "")
	fs.IntVar(&cfg.Jobs, "jobs", 0, "")

	if err := fs.Parse(args); err != nil {
		return cfg, false, err
	}
	if fs.NArg() > 1 {
		return cfg, false, fmt.Errorf("unexpected argument %q", fs.Arg(1))
	}
	cfg.Path = fs.Arg(0) // empty if not given

	if cfg.BranchReportLimit < 0 {
		return cfg, false, errors.New("--branch-report-limit must be non-negative")
	}
	if cfg.Jobs < 0 {
		return cfg, false, errors.New("--jobs must be non-negative")
	}
	cfg.Fetch = cfg.Fetch || cfg.Pull
	return cfg, verbose, nil
}
