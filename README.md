# repocheck

Finds work in git repositories that hasn't been committed, pushed or pulled.

## Usage

```bash
$ repocheck --help
A utility to check the health of the git repositories in a directory.

Usage: repocheck [OPTIONS] [PATH]

Options:
  -v, --verbose                Show every check, not just the ones with problems
      --depth N                Max directory levels below PATH to search, 0 = no limit (default: 0)
      --follow-symlinks        Include symlinked repositories
      --fetch                  Run 'git fetch --all --prune' first, so ahead/behind counts are current
      --pull                   Fetch, then fast-forward the checked-out branch if the working tree is clean
      --branch-report-limit N  Max number of branches listed per check (default: 10)
  -j, --jobs N                 Number of repositories to check in parallel, 0 = one per CPU (default: 0)
  -h, --help                   Show this help message

Exits with 0 if every check passed, 1 if any check failed, or 2 on errors. A failed fetch is only a warning.
```

```bash
$ repocheck ~/Projects
api: OK

dotfiles:
  FAILED  Working tree clean: 2 changed files
  FAILED  No stashes: 1 stash

website:
  FAILED  All branches pushed: redesign
  FAILED  All branches tracked: redesign

3 repos, 2 with problems, 0 errors
```

---

Each repository is checked for:

- A remote
- Changes to tracked files, untracked files and stashes
- Branches with commits that aren't on any remote
- Branches that are behind or diverged from their upstream, or have no upstream
- Commits on a detached HEAD that aren't on any branch

A directory is checked if it contains a `.git` directory. Every directory under `PATH` is searched, except repositories
(so nested repositories and submodules aren't checked). `--follow-symlinks` only follows to repositories.

`--pull` fast-forwards the checked-out branch, and only if it has no local commits and no changes to tracked files.

Output is colored when writing to a terminal, unless `NO_COLOR` is set.

## Installation

```bash
go build      # Compile the program
go install .  # Install the binary to $(go env GOPATH)/bin
```
