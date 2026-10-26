# honk

A CLI for keeping tabs on a directory full of git repos. Point it at a folder and it finds every repo underneath, shows each one's branch, uncommitted changes, and how stale it is, and can bring stale branches up to date with their base branch by merging or rebasing.

```
$ honk ~/code
REPO            BRANCH             CHANGES    UPSTREAM  BASE               REBASE         AGE  LAST COMMIT
api             feature/billing    clean      =         origin/main ↑3 ↓7  ready          2h   4f1c2aa add invoice export
web             feature/clash      clean      =         origin/main ↑1 ↓4  conflicts (2)  1d   65b1887 rework nav
infra           main               clean      ↓2        origin/main ↓2     fast-forward   3d   9ec22ac bump runners
scratch         wip                +1 ~2 ?1   -         origin/main ↑1 ↓4  dirty          now  598b255 spike
tools/linter    main               clean      =         origin/main =      up-to-date     2mo  6df0b9b release 1.4

5 repos · 1 with changes · 4 behind · 2 ready to rebase · 1 with conflicts
```

## Install

```sh
go install github.com/immannino/honk@latest
```

You need Go 1.26+ to build it and git 2.38+ to run it. Make sure `$(go env GOPATH)/bin` is on your `PATH`.

## `honk status`: see where everything stands

`status` is the default command, so `honk`, `honk status`, and `honk st` all do the same thing. The directory defaults to `.`.

```sh
honk                       # everything under the current directory
honk ~/code --fetch        # fetch first so behind-counts reflect the remote
honk ~/code --dirty        # only repos with uncommitted changes
honk ~/code --behind       # only repos behind their upstream or base
honk ~/code --only 'api*'  # only repos whose relative path matches a glob
honk ~/code --json         # machine-readable output
```

| Column | Meaning |
| --- | --- |
| **CHANGES** | `!` conflicted, `+` staged, `~` modified, `?` untracked, `*` stashes. `clean` means none of these. |
| **UPSTREAM** | Commits ahead (`↑`) or behind (`↓`) the branch's own remote branch. `-` means the branch has never been pushed. |
| **BASE** | Ahead/behind the base branch. The base is `<remote>/HEAD`, falling back to `main` then `master`. Override it with `--base develop`. |
| **REBASE** | Whether the branch can be brought up to date. See below. |

| REBASE state | Meaning |
| --- | --- |
| `up-to-date` | Already contains everything on base. |
| `fast-forward` | Has no commits of its own, so it can just move forward. |
| `ready` | Clean working tree, and merging base in would not conflict. |
| `conflicts (N)` | Would conflict in N files. |
| `dirty` | Has uncommitted changes that need committing or stashing first. |
| `busy` | A merge, rebase, cherry-pick, etc. is already in progress. |
| `detached` / `no-base` | Not on a branch, or no base branch was found. |

Conflict prediction uses `git merge-tree`, which runs the merge in memory without touching your working tree or index. A clean merge is a strong but imperfect predictor of a clean rebase, because a rebase replays commits one at a time and an intermediate commit can still conflict. Uncommitted changes aren't part of the prediction.

## `honk sync`: bring stale branches up to date

`sync` fetches every repo, shows a plan, and asks before changing anything. It only works on the branch each repo has checked out.

```
$ honk sync ~/code
REPO     BRANCH           ACTION        DETAILS
api      feature/billing  merge         7 commit(s) from origin/main
web      feature/clash    skip          would conflict in src/nav.tsx, src/app.tsx
infra    main             fast-forward  2 commit(s) from origin/main
scratch  wip              skip          uncommitted changes (commit, stash, or use --autostash)

2 to update · 2 need attention · 1 already up to date

Apply 2 update(s) using merge? [y/N]
```

### Merge or rebase

| | `--strategy merge` (default) | `--strategy rebase` / `-r` |
| --- | --- | --- |
| Does | `git merge origin/main`, the `git pull origin main` workflow | `git rebase origin/main` |
| Commit message | Identical to what `git pull origin main` writes | n/a |
| With `--push` | `git push` | `git push --force-with-lease` |

If you always rebase, set it once in your shell profile:

```sh
export HONK_STRATEGY=rebase
```

Either way, branches with no commits of their own are fast-forwarded.

### Pushing is opt-in

Without `--push`, every change stays local. With it:

- Updated branches are pushed to their upstream. Rebased branches are pushed with `--force-with-lease`, which refuses to overwrite remote commits you haven't fetched.
- Branches that are already up to date with base but have unpushed commits get pushed too. That lets you sync, look things over, then run `honk sync --push`. Note that this pushes **every** unpushed commit on those branches, not just the merge.
- Branches that have never been pushed stay local. honk won't publish a new branch for you.

```sh
honk sync ~/code              # update locally, review
honk sync ~/code --push       # then publish
honk sync ~/code -r --push -y # rebase and force-push (with lease), no prompt
```

### What gets skipped

A repo is left untouched, and listed with the reason, when:

- merging or rebasing would conflict
- it has uncommitted changes (pass `--autostash` to stash them around the update instead)
- a merge, rebase, or similar is already in progress, or HEAD is detached
- someone else pushed to your branch since you last pulled. Pull those commits first.
- with the merge strategy, the branch was rebased since it was last pushed. Use `--rebase` to force-push it.

If a merge or rebase stops part-way anyway, honk aborts it, so the branch is exactly as it was. If a push fails, the local update is kept and both commit IDs are reported so you can retry. `sync` exits non-zero if anything failed.

### Flags

```
--strategy, -s   merge (default) or rebase                    [$HONK_STRATEGY]
--rebase, -r     shorthand for --strategy rebase
--push, -p       push updated branches (rebased ones with --force-with-lease)
--autostash      stash uncommitted changes around the update instead of skipping
--dry-run, -n    show the plan and exit
--yes, -y        don't ask for confirmation
--no-fetch       use remote-tracking refs as they are
```

## Shared flags

These work with both `status` and `sync`:

```
--base, -b       base branch (default: <remote>/HEAD, then main, then master)
--only, -o       only repos whose relative path matches this glob (repeatable)
--depth, -d      how many directories deep to search (default 5, 0 = unlimited)
--skip           directory names to skip (default: node_modules, vendor, .venv, venv, .terraform, .cache)
--nested         keep searching inside repos (submodules, repos within repos)
--jobs, -j       repos processed concurrently (default: 2×CPUs, max 16)
--fetch-timeout  per-repo fetch timeout (default 30s)
--no-color       disable color (NO_COLOR is honored too)
```

## How it behaves

- **It runs the git CLI, not a reimplementation**, so your config, credential helpers, hooks, and merge settings all apply.
- **It never prompts.** git runs with `GIT_TERMINAL_PROMPT=0` and SSH in batch mode, so a repo that needs credentials fails fast instead of hanging the run.
- **It doesn't get in your editor's way.** `status` runs with `GIT_OPTIONAL_LOCKS=0`, so it won't fight an IDE that has the repo open.

## Development

```sh
make            # list targets
make check      # what CI runs: gofmt check, go vet, tests with -race
make test-ci    # tests with your personal git config hidden
make test-linux # tests in a Linux container with no git identity (needs Docker)
make build      # ./honk
```

The tests build real repos with real remotes in temp directories, so they need `git` on your `PATH`. `make test-linux` is the closest match to the CI runner. On macOS, git falls back to your account's full name when no identity is configured, which can hide failures that only show up on Linux.

## License

[MIT](LICENSE)
