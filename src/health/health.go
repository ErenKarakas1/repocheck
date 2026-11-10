package health

import (
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
)

type Config struct {
	Path              string
	Depth             int // how many directory levels below Path are searched for repositories, 0 = no limit
	FollowSymlinks    bool
	Fetch             bool // run `git fetch --all --prune` first, so ahead/behind counts are current
	Pull              bool // fast-forward the checked-out branch when the working tree is clean
	BranchReportLimit int  // max number of branches listed per check
	Jobs              int  // number of repositories checked in parallel, 0 = one per CPU
}

type Check string

const (
	HasRemote          Check = "Has remote"
	FetchRemotes       Check = "Fetch remotes"
	PullCurrentBranch  Check = "Pull current branch"
	WorkingTreeClean   Check = "Working tree clean"
	NoUntrackedFiles   Check = "No untracked files"
	NoStashes          Check = "No stashes"
	AllBranchesPushed  Check = "All branches pushed"
	NoDivergedBranches Check = "No diverged branches"
	NoBranchesBehind   Check = "No branches behind upstream"
	AllBranchesTracked Check = "All branches tracked"
	NoDetachedCommits  Check = "No detached commits"
)

type Status int

// ordered by severity
const (
	Passed  Status = iota
	Warning        // the check couldn't be run, but the others are still accurate, e.g. a fetch while offline
	Failed
	Errored // the check couldn't be run, e.g. git refused to fast-forward
)

var statusNames = [...]string{
	Passed:  "PASSED",
	Warning: "WARN",
	Failed:  "FAILED",
	Errored: "ERROR",
}

func (s Status) String() string {
	return statusNames[s]
}

type CheckResult struct {
	Check  Check
	Status Status
	Detail string // what failed, e.g. "2 stashes" or "main (-1), dev (-3)"
}

type RepoResult struct {
	Name   string // path relative to Config.Path
	Path   string
	Checks []CheckResult
	Err    error // git's error message if it couldn't check the repository
}

// returns the most severe status of the repository's checks
func (r RepoResult) Status() Status {
	if r.Err != nil {
		return Errored
	}
	status := Passed
	for _, c := range r.Checks {
		status = max(status, c.Status)
	}
	return status
}

func CheckRepos(cfg Config) ([]RepoResult, error) {
	depth := cfg.Depth
	if depth <= 0 {
		depth = math.MaxInt
	}
	paths, err := findRepos(cfg.Path, depth, cfg.FollowSymlinks)
	if err != nil {
		return nil, err
	}

	jobs := cfg.Jobs
	if jobs <= 0 {
		jobs = runtime.NumCPU()
	}
	jobs = min(jobs, len(paths))

	results := make([]RepoResult, len(paths))
	indices := make(chan int)
	var wg sync.WaitGroup
	for range jobs {
		wg.Go(func() {
			for i := range indices {
				checks, err := checkRepo(paths[i], cfg)
				name, _ := filepath.Rel(cfg.Path, paths[i])
				results[i] = RepoResult{Name: name, Path: paths[i], Checks: checks, Err: err}
			}
		})
	}
	for i := range paths {
		indices <- i
	}
	close(indices)
	wg.Wait()
	return results, nil
}

// returns the git repositories under dir, up to depth levels down, sorted by path
//
// repositories aren't searched for nested ones (e.g. submodules), and symlinks are
// only followed to repositories, so a link loop can't make it search forever
func findRepos(dir string, depth int, followSymlinks bool) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	var repos []string
	for _, entry := range entries {
		isLink := entry.Type()&fs.ModeSymlink != 0
		if !entry.IsDir() && !(isLink && followSymlinks) {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		if isGitRepo(path) {
			repos = append(repos, path)
		} else if entry.IsDir() && depth > 1 {
			nested, _ := findRepos(path, depth-1, followSymlinks)
			repos = append(repos, nested...)
		}
	}
	return repos, nil
}

// this is a naive check, but it should be sufficient for this utility
func isGitRepo(path string) bool {
	info, err := os.Stat(filepath.Join(path, ".git"))
	return err == nil && info.IsDir()
}

func checkRepo(repo string, cfg Config) ([]CheckResult, error) {
	remotes, err := git(repo, "remote")
	if err != nil {
		return nil, err
	}
	hasRemote := remotes != ""

	var checks []CheckResult
	if hasRemote {
		checks = append(checks, pass(HasRemote))
	} else {
		checks = append(checks, fail(HasRemote, ""))
	}

	// a failed fetch (offline, no access) is a warning. the local checks still run against what was last fetched
	if cfg.Fetch && hasRemote {
		if _, err := git(repo, "fetch", "--all", "--prune", "--quiet"); err != nil {
			checks = append(checks, CheckResult{Check: FetchRemotes, Status: Warning, Detail: err.Error()})
		} else {
			checks = append(checks, pass(FetchRemotes))
		}
	}

	wt, err := workingTreeStatus(repo)
	if err != nil {
		return nil, err
	}
	branches, err := listBranches(repo)
	if err != nil {
		return nil, err
	}

	if cfg.Pull && hasRemote {
		if pulled, ok := pullCurrentBranch(repo, wt, branches); ok {
			checks = append(checks, pulled)
		}
	}

	checks = append(checks,
		checkCount(WorkingTreeClean, wt.changed, "changed file", "changed files"),
		checkCount(NoUntrackedFiles, wt.untracked, "untracked path", "untracked paths"),
		checkCount(NoStashes, wt.stashes, "stash", "stashes"),
	)
	checks = append(checks, checkBranches(branches, cfg.BranchReportLimit)...)

	detached := pass(NoDetachedCommits)
	if wt.detached {
		n, err := detachedCommits(repo)
		if err != nil {
			return nil, err
		}
		if n > 0 {
			detached = fail(NoDetachedCommits, fmt.Sprintf("HEAD (+%d)", n))
		}
	}
	return append(checks, detached), nil
}

func pullCurrentBranch(repo string, wt workingTree, branches []branch) (CheckResult, bool) {
	i := slices.IndexFunc(branches, func(b branch) bool { return b.isHead })
	if i < 0 {
		return CheckResult{}, false
	}
	head := &branches[i]
	if head.behind == 0 || head.ahead > 0 {
		return CheckResult{}, false
	}

	// untracked files are fine, merge refuses to overwrite them
	if wt.changed > 0 {
		return CheckResult{}, false
	}

	if _, err := git(repo, "merge", "--ff-only", "--quiet", "@{upstream}"); err != nil {
		return CheckResult{Check: PullCurrentBranch, Status: Errored, Detail: err.Error()}, true
	}

	detail := fmt.Sprintf("%s, %s", head.name, pluralize(head.behind, "new commit", "new commits"))
	head.behind = 0
	return CheckResult{Check: PullCurrentBranch, Status: Passed, Detail: detail}, true
}

func checkBranches(branches []branch, limit int) []CheckResult {
	var unpushed, diverged, behind, untracked []string
	for _, b := range branches {
		if b.unpushed {
			unpushed = append(unpushed, b.name)
		}

		switch {
		case b.ahead > 0 && b.behind > 0:
			diverged = append(diverged, fmt.Sprintf("%s (+%d/-%d)", b.name, b.ahead, b.behind))
		case b.behind > 0:
			behind = append(behind, fmt.Sprintf("%s (-%d)", b.name, b.behind))
		}

		switch {
		case b.upstream == "":
			untracked = append(untracked, b.name)
		case b.upstreamGone:
			untracked = append(untracked, b.name+" (upstream gone)")
		}
	}

	return []CheckResult{
		checkItems(AllBranchesPushed, unpushed, limit),
		checkItems(NoDivergedBranches, diverged, limit),
		checkItems(NoBranchesBehind, behind, limit),
		checkItems(AllBranchesTracked, untracked, limit),
	}
}

func pass(c Check) CheckResult {
	return CheckResult{Check: c, Status: Passed}
}

func fail(c Check, detail string) CheckResult {
	return CheckResult{Check: c, Status: Failed, Detail: detail}
}

func checkCount(c Check, n int, singular, plural string) CheckResult {
	if n == 0 {
		return pass(c)
	}
	return fail(c, pluralize(n, singular, plural))
}

func checkItems(c Check, items []string, limit int) CheckResult {
	if len(items) == 0 {
		return pass(c)
	}
	if extra := len(items) - limit; extra > 0 {
		items = append(items[:limit], fmt.Sprintf("+%d more", extra))
	}
	return fail(c, strings.Join(items, ", "))
}

func pluralize(n int, singular, plural string) string {
	if n == 1 {
		return "1 " + singular
	}
	return fmt.Sprintf("%d %s", n, plural)
}
