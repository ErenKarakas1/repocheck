package health

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
)

func git(repo string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0",
		"GIT_OPTIONAL_LOCKS=0",
	)

	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return "", errors.New(msg)
		}
		return "", fmt.Errorf("git %s: %w", args[0], err)
	}
	return stdout.String(), nil
}

func lines(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool { return r == '\n' })
}

type workingTree struct {
	changed   int // changes to tracked files
	untracked int
	stashes   int
	detached  bool // HEAD isn't on a branch
}

func workingTreeStatus(repo string) (workingTree, error) {
	out, err := git(repo, "status", "--porcelain=v2", "--branch", "--show-stash")
	if err != nil {
		return workingTree{}, err
	}
	return parseStatus(out)
}

// parses `git status --porcelain=v2 --branch --show-stash`, e.g.
// "# branch.head main\n# stash 2\n1 .M N... <modes and hashes> file\n? new".
func parseStatus(out string) (workingTree, error) {
	var wt workingTree
	for _, line := range lines(out) {
		if n, ok := strings.CutPrefix(line, "# stash "); ok {
			var err error
			if wt.stashes, err = strconv.Atoi(n); err != nil {
				return workingTree{}, fmt.Errorf("unexpected status line: %q", line)
			}
		} else if line == "# branch.head (detached)" {
			wt.detached = true
		} else if strings.HasPrefix(line, "? ") {
			wt.untracked++
		} else if !strings.HasPrefix(line, "#") { // other headers
			wt.changed++
		}
	}
	return wt, nil
}

type branch struct {
	name         string
	isHead       bool   // currently checked out
	commit       string // hash of the branch tip
	upstream     string // full ref name, e.g. refs/remotes/origin/main; empty if no upstream is configured
	upstreamGone bool   // upstream was configured but has since been deleted
	ahead        int    // commits that aren't on the upstream
	behind       int    // upstream commits that aren't on the branch
	unpushed     bool   // has commits that aren't on any remote
}

const branchFormat = "%(HEAD)%09%(refname:lstrip=2)%09%(objectname)%09%(upstream)%09%(upstream:track,nobracket)"

func listBranches(repo string) ([]branch, error) {
	out, err := git(repo, "for-each-ref", "--format="+branchFormat, "refs/heads")
	if err != nil {
		return nil, err
	}

	var branches []branch
	for _, line := range lines(out) {
		b, err := parseBranch(line)
		if err != nil {
			return nil, err
		}
		branches = append(branches, b)
	}

	// a branch can be pushed without being on its upstream, e.g. one created from origin/main tracks it, but may be
	// pushed to origin/feature. so check whether its tip is on any remote instead.
	if slices.ContainsFunc(branches, mayBeUnpushed) {
		unpushed, err := unpushedCommits(repo)
		if err != nil {
			return nil, err
		}
		for i := range branches {
			branches[i].unpushed = unpushed[branches[i].commit]
		}
	}
	return branches, nil
}

// reports whether a branch might have commits that aren't on any remote
func mayBeUnpushed(b branch) bool {
	return b.ahead > 0 || b.upstreamGone || !strings.HasPrefix(b.upstream, "refs/remotes/")
}

// parses a line of for-each-ref output in branchFormat, e.g.
// "*\tmain\t<hash>\trefs/remotes/origin/main\tahead 1, behind 2".
func parseBranch(line string) (branch, error) {
	fields := strings.Split(line, "\t") // tabs can't appear in ref names
	if len(fields) != 5 {
		return branch{}, fmt.Errorf("unexpected for-each-ref line: %q", line)
	}

	b := branch{name: fields[1], isHead: fields[0] == "*", commit: fields[2], upstream: fields[3]}
	for item := range strings.SplitSeq(fields[4], ", ") {
		var err error
		if n, ok := strings.CutPrefix(item, "ahead "); ok {
			b.ahead, err = strconv.Atoi(n)
		} else if n, ok := strings.CutPrefix(item, "behind "); ok {
			b.behind, err = strconv.Atoi(n)
		} else if item == "gone" {
			b.upstreamGone = true
		} else if item != "" { // empty when up to date or there's no upstream
			err = errors.New("unknown tracking status")
		}
		if err != nil {
			return branch{}, fmt.Errorf("unexpected for-each-ref line: %q", line)
		}
	}
	return b, nil
}

// returns the commits on local branches that aren't on any remote. a branch whose tip isn't returned
// is fully pushed, since the remote that has the tip has everything before it too
func unpushedCommits(repo string) (map[string]bool, error) {
	out, err := git(repo, "rev-list", "--branches", "--not", "--remotes")
	if err != nil {
		return nil, err
	}

	commits := make(map[string]bool)
	for _, commit := range lines(out) {
		commits[commit] = true
	}
	return commits, nil
}

// returns the number of commits on a detached HEAD that aren't on any branch or remote
func detachedCommits(repo string) (int, error) {
	out, err := git(repo, "rev-list", "--count", "HEAD", "--not", "--branches", "--remotes")
	if err != nil {
		return 0, err
	}
	n, err := strconv.Atoi(strings.TrimSpace(out))
	if err != nil {
		return 0, fmt.Errorf("unexpected rev-list output: %q", out)
	}
	return n, nil
}
