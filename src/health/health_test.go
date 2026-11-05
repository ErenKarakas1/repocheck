package health

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestParseBranch(t *testing.T) {
	tests := []struct {
		line string
		want branch
	}{
		{"*\tmain\tc1\trefs/remotes/origin/main\t", branch{name: "main", isHead: true, commit: "c1", upstream: "refs/remotes/origin/main"}},
		{" \tdev\tc2\trefs/remotes/origin/dev\tahead 2", branch{name: "dev", commit: "c2", upstream: "refs/remotes/origin/dev", ahead: 2}},
		{" \tdev\tc2\trefs/remotes/origin/dev\tbehind 3", branch{name: "dev", commit: "c2", upstream: "refs/remotes/origin/dev", behind: 3}},
		{" \tdev\tc2\trefs/remotes/origin/dev\tahead 2, behind 3", branch{name: "dev", commit: "c2", upstream: "refs/remotes/origin/dev", ahead: 2, behind: 3}},
		{" \told\tc3\trefs/remotes/origin/old\tgone", branch{name: "old", commit: "c3", upstream: "refs/remotes/origin/old", upstreamGone: true}},
		{" \tfeature/x\tc4\t\t", branch{name: "feature/x", commit: "c4"}},
	}
	for _, tt := range tests {
		got, err := parseBranch(tt.line)
		if err != nil {
			t.Errorf("parseBranch(%q): %v", tt.line, err)
		} else if got != tt.want {
			t.Errorf("parseBranch(%q) = %+v, want %+v", tt.line, got, tt.want)
		}
	}

	for _, line := range []string{"main", " \tdev\tc2\torigin/dev", " \tdev\tc2\torigin/dev\tahead x", " \tdev\tc2\torigin/dev\tsideways 1"} {
		if _, err := parseBranch(line); err == nil {
			t.Errorf("parseBranch(%q) succeeded, want error", line)
		}
	}
}

func TestMayBeUnpushed(t *testing.T) {
	tests := []struct {
		b    branch
		want bool
	}{
		{branch{upstream: "refs/remotes/origin/main"}, false},
		{branch{upstream: "refs/remotes/origin/main", behind: 1}, false},
		{branch{upstream: "refs/remotes/origin/main", ahead: 1}, true},
		{branch{upstream: "refs/remotes/origin/gone", upstreamGone: true}, true},
		{branch{upstream: "refs/heads/main"}, true}, // tracks a local branch
		{branch{}, true},
	}
	for _, tt := range tests {
		if got := mayBeUnpushed(tt.b); got != tt.want {
			t.Errorf("mayBeUnpushed(%+v) = %v, want %v", tt.b, got, tt.want)
		}
	}
}

func TestParseStatus(t *testing.T) {
	out := `# branch.oid 4501fc
# branch.head main
# branch.upstream origin/main
# branch.ab +0 -0
# stash 3
1 .M N... 100644 100644 100644 4501fc 4501fc changed
2 R. N... 100644 100644 100644 4501fc 4501fc R100 renamed	old
u UU N... 100644 100644 100644 100644 4501fc 4501fc 4501fc conflicted
? untracked
? dir/
`
	want := workingTree{changed: 3, untracked: 2, stashes: 3}
	if got, err := parseStatus(out); err != nil || got != want {
		t.Errorf("parseStatus() = %+v, %v, want %+v", got, err, want)
	}
	if got, err := parseStatus(""); err != nil || got != (workingTree{}) {
		t.Errorf("parseStatus(\"\") = %+v, %v, want zero", got, err)
	}
	out = "# branch.oid 4501fc\n# branch.head (detached)\n"
	if got, err := parseStatus(out); err != nil || got != (workingTree{detached: true}) {
		t.Errorf("parseStatus(%q) = %+v, %v, want detached", out, got, err)
	}
	if _, err := parseStatus("# stash x\n"); err == nil {
		t.Error("parseStatus with a bad stash count succeeded, want error")
	}
}

func TestCheckBranches(t *testing.T) {
	branches := []branch{
		{name: "main", upstream: "refs/remotes/origin/main"},
		{name: "ahead", upstream: "refs/remotes/origin/ahead", ahead: 1, unpushed: true},
		{name: "behind", upstream: "refs/remotes/origin/behind", behind: 2},
		{name: "diverged", upstream: "refs/remotes/origin/diverged", ahead: 3, behind: 4, unpushed: true},
		{name: "local", unpushed: true},
		{name: "gone", upstream: "refs/remotes/origin/gone", upstreamGone: true},
		{name: "feature", upstream: "refs/remotes/origin/main", ahead: 2}, // pushed somewhere other than its upstream
	}
	want := []CheckResult{
		{AllBranchesPushed, Failed, "ahead, diverged, +1 more"},
		{NoDivergedBranches, Failed, "diverged (+3/-4)"},
		{NoBranchesBehind, Failed, "behind (-2)"},
		{AllBranchesTracked, Failed, "local, gone (upstream gone)"},
	}
	if got := checkBranches(branches, 2); !slices.Equal(got, want) {
		t.Errorf("checkBranches() =\n%v\nwant\n%v", got, want)
	}

	want = []CheckResult{pass(AllBranchesPushed), pass(NoDivergedBranches), pass(NoBranchesBehind), pass(AllBranchesTracked)}
	if got := checkBranches(branches[:1], 2); !slices.Equal(got, want) {
		t.Errorf("checkBranches() with no problems =\n%v\nwant\n%v", got, want)
	}
}

func TestCheckItems(t *testing.T) {
	items := []string{"a", "b", "c"}
	for limit, want := range map[int]string{0: "+3 more", 1: "a, +2 more", 3: "a, b, c", 10: "a, b, c"} {
		if got := checkItems(NoStashes, slices.Clone(items), limit); got.Detail != want {
			t.Errorf("checkItems(%q, %d) = %q, want %q", items, limit, got.Detail, want)
		}
	}
}

func TestWriteReport(t *testing.T) {
	repos := []RepoResult{
		{Path: "/src/broken", Err: errors.New("fatal: not a git repository\nhint: really")},
		{Path: "/src/fine", Checks: []CheckResult{pass(HasRemote), pass(NoStashes)}},
		{Path: "/src/messy", Checks: []CheckResult{
			pass(HasRemote),
			{FetchRemotes, Warning, "fatal: offline\nfatal: really"},
			fail(NoStashes, "2 stashes"),
		}},
		{Path: "/src/offline", Checks: []CheckResult{pass(HasRemote), {FetchRemotes, Warning, "fatal: offline"}}},
	}

	var b strings.Builder
	if err := WriteReport(&b, repos, ReportOptions{}); err != nil {
		t.Fatal(err)
	}
	want := `broken: ERROR
    fatal: not a git repository
    hint: really

fine: OK

messy:
  WARN    Fetch remotes: fatal: offline
    fatal: really
  FAILED  No stashes: 2 stashes

offline:
  WARN    Fetch remotes: fatal: offline

4 repos, 1 with problems, 1 error, 2 warnings
`
	if b.String() != want {
		t.Errorf("WriteReport() =\n%s\nwant\n%s", b.String(), want)
	}

	b.Reset()
	if err := WriteReport(&b, repos[1:2], ReportOptions{Verbose: true}); err != nil {
		t.Fatal(err)
	}
	want = `/src/fine:
  PASSED  Has remote
  PASSED  No stashes

1 repo, 0 with problems, 0 errors
`
	if b.String() != want {
		t.Errorf("verbose WriteReport() =\n%s\nwant\n%s", b.String(), want)
	}

	b.Reset()
	if err := WriteReport(&b, repos[:3], ReportOptions{Color: true}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"broken: \x1b[1;31mERROR\x1b[0m\n", "fine: \x1b[32mOK\x1b[0m\n",
		"  \x1b[33mWARN  \x1b[0m  Fetch remotes", "  \x1b[31mFAILED\x1b[0m  No stashes"} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("colored WriteReport() =\n%q\nwant it to contain %q", b.String(), want)
		}
	}
}

// TestCheckRepos runs the checks against real repositories.
func TestCheckRepos(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found")
	}
	// Isolate from the user's git config
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_AUTHOR_NAME", "test")
	t.Setenv("GIT_AUTHOR_EMAIL", "test@example.com")
	t.Setenv("GIT_COMMITTER_NAME", "test")
	t.Setenv("GIT_COMMITTER_EMAIL", "test@example.com")

	runGit := func(dir string, args ...string) {
		t.Helper()
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	writeFile := func(path, content string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	commit := func(repo, msg string) {
		t.Helper()
		writeFile(filepath.Join(repo, msg), msg)
		runGit(repo, "add", ".")
		runGit(repo, "commit", "-m", msg)
	}

	// A bare remote, and a clone of it outside the checked directory to push new commits from
	root := t.TempDir()
	dir := filepath.Join(root, "repos")
	remote := filepath.Join(root, "remote.git")
	pusher := filepath.Join(root, "pusher")
	runGit(root, "init", "--bare", "-b", "main", remote)
	runGit(root, "clone", remote, pusher)
	commit(pusher, "first")
	runGit(pusher, "push", "origin", "main")

	if err := os.MkdirAll(filepath.Join(dir, "notrepo"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"badremote", "behind", "clean", "detached", "dirty", "triangular"} {
		runGit(root, "clone", remote, filepath.Join(dir, name))
	}

	// Fetching fails, but the last fetched state is still there
	runGit(filepath.Join(dir, "badremote"), "remote", "set-url", "origin", filepath.Join(root, "missing.git"))

	detached := filepath.Join(dir, "detached")
	runGit(detached, "checkout", "--detach")
	commit(detached, "lost")

	dirty := filepath.Join(dir, "dirty")
	writeFile(filepath.Join(dirty, "first"), "stashed")
	runGit(dirty, "stash")
	writeFile(filepath.Join(dirty, "first"), "changed")
	writeFile(filepath.Join(dirty, "untracked"), "")

	local := filepath.Join(dir, "local")
	runGit(root, "init", "-b", "main", local)
	commit(local, "one")
	commit(local, "two")
	runGit(local, "branch", "--track", "localtrack", "main") // tracked, but its upstream isn't on a remote

	// A branch that tracks origin/main, but is pushed to origin/feat
	triangular := filepath.Join(dir, "triangular")
	runGit(triangular, "checkout", "-b", "feat", "origin/main")
	commit(triangular, "feat")
	runGit(triangular, "push", "origin", "feat")

	commit(pusher, "second") // only visible to the other repos after a fetch
	runGit(pusher, "push", "origin", "main")

	cfg := Config{Path: dir, BranchReportLimit: 10, Jobs: 2}
	repos, err := CheckRepos(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	if err := WriteReport(&b, repos, ReportOptions{}); err != nil {
		t.Fatal(err)
	}
	want := `badremote: OK

behind: OK

clean: OK

detached:
  FAILED  No detached commits: HEAD (+1)

dirty:
  FAILED  Working tree clean: 1 changed file
  FAILED  No untracked files: 1 untracked path
  FAILED  No stashes: 1 stash

local:
  FAILED  Has remote
  FAILED  All branches pushed: localtrack, main
  FAILED  All branches tracked: main

triangular: OK

7 repos, 3 with problems, 0 errors
`
	if b.String() != want {
		t.Errorf("report =\n%s\nwant\n%s", b.String(), want)
	}

	// Pulling fast-forwards the clean clones, but leaves the dirty one behind
	cfg.Fetch, cfg.Pull = true, true
	if repos, err = CheckRepos(cfg); err != nil {
		t.Fatal(err)
	}
	byName := make(map[string]RepoResult)
	for _, repo := range repos {
		byName[filepath.Base(repo.Path)] = repo
	}
	pulled := CheckResult{PullCurrentBranch, Passed, "main, 1 new commit"}
	for _, name := range []string{"behind", "clean"} {
		if repo := byName[name]; !slices.Contains(repo.Checks, pulled) || repo.Status() != Passed {
			t.Errorf("%s wasn't pulled cleanly: %+v", name, repo.Checks)
		}
	}
	if behind := fail(NoBranchesBehind, "main (-1)"); !slices.Contains(byName["dirty"].Checks, behind) {
		t.Errorf("dirty repo checks = %+v, want %+v", byName["dirty"].Checks, behind)
	}
	if repo := byName["badremote"]; repo.Status() != Warning || repo.Checks[1].Check != FetchRemotes {
		t.Errorf("badremote = %v, %+v, want only a fetch warning", repo.Status(), repo.Checks)
	}
}
