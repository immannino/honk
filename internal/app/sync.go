package app

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/urfave/cli/v3"

	"github.com/immannino/honk/internal/report"
	"github.com/immannino/honk/internal/syncer"
)

func syncCommand() *cli.Command {
	return &cli.Command{
		Name:      "sync",
		Aliases:   []string{"backmerge"},
		Usage:     "bring stale branches up to date with their base branch (merge or rebase)",
		ArgsUsage: "[DIR]",
		Description: `Fetches every repo under DIR, then for each checked-out branch that is behind
its base branch:

  merge  (default)  git merge <base> — the "git pull origin main" workflow,
                    including pull's merge commit message
  rebase            git rebase <base>

Branches with no commits of their own are fast-forwarded either way.

Changes stay local unless you pass --push. With --push, merged branches are
pushed normally and rebased branches with --force-with-lease, which refuses to
overwrite remote commits you haven't fetched. --push also publishes branches
that are already up to date with base but have unpushed commits, so you can
sync now, look things over, and push later. Note that it pushes every unpushed
commit on such a branch, not just the merge.

Repos are skipped, never touched, when they have uncommitted changes (unless
--autostash), would conflict, are mid-operation, or have new commits on their
own upstream that you haven't pulled. A merge or rebase that fails part-way is
aborted, leaving the branch exactly as it was.

You'll see the plan and be asked to confirm before anything changes.`,
		Flags: append([]cli.Flag{
			&cli.StringFlag{
				Name:    "strategy",
				Aliases: []string{"s"},
				Value:   string(syncer.StrategyMerge),
				Usage:   "how to pick up base commits: merge or rebase",
				Sources: cli.EnvVars("HONK_STRATEGY"),
			},
			&cli.BoolFlag{Name: "rebase", Aliases: []string{"r"}, Usage: "shorthand for --strategy rebase"},
			&cli.BoolFlag{Name: "push", Aliases: []string{"p"}, Usage: "push updated branches to their upstream (rebased branches use --force-with-lease)"},
			&cli.BoolFlag{Name: "autostash", Usage: "stash uncommitted changes around the update instead of skipping the repo"},
			&cli.BoolFlag{Name: "no-fetch", Usage: "use remote-tracking refs as they are instead of fetching first"},
			&cli.BoolFlag{Name: "dry-run", Aliases: []string{"n"}, Usage: "show the plan and exit"},
			&cli.BoolFlag{Name: "yes", Aliases: []string{"y"}, Usage: "apply without asking for confirmation"},
		}, repoFlags()...),
		Action: runSync,
	}
}

func runSync(ctx context.Context, cmd *cli.Command) error {
	strategy, err := syncer.ParseStrategy(cmd.String("strategy"))
	if err != nil {
		return err
	}
	if cmd.Bool("rebase") {
		strategy = syncer.StrategyRebase
	}

	absRoot, statuses, err := collect(ctx, cmd, !cmd.Bool("no-fetch"))
	if err != nil {
		return err
	}
	if len(statuses) == 0 {
		fmt.Fprintf(os.Stderr, "no matching git repositories under %s\n", absRoot)
		return nil
	}

	opts := syncer.Options{Strategy: strategy, Push: cmd.Bool("push"), Autostash: cmd.Bool("autostash")}
	var steps, todo []syncer.Step
	for _, st := range statuses {
		step := syncer.Plan(st, opts)
		steps = append(steps, step)
		if step.Actionable() {
			todo = append(todo, step)
		}
	}

	color := useColor(cmd)
	if err := report.Plan(os.Stdout, steps, color); err != nil {
		return err
	}
	if len(todo) == 0 || cmd.Bool("dry-run") {
		return nil
	}

	if !cmd.Bool("yes") {
		if !stdinIsTerminal() {
			fmt.Fprintln(os.Stderr, "\nnot a terminal; re-run with --yes to apply")
			return nil
		}
		if !confirm(fmt.Sprintf("\nApply %d update(s) using %s? [y/N] ", len(todo), strategy)) {
			fmt.Println("nothing changed")
			return nil
		}
	}
	fmt.Println()

	results := applyAll(ctx, todo, cmd.Int("jobs"))
	if err := report.Results(os.Stdout, results, color); err != nil {
		return err
	}
	for _, r := range results {
		if !r.OK() {
			return cli.Exit("", 1)
		}
	}
	return nil
}

func applyAll(ctx context.Context, steps []syncer.Step, jobs int) []syncer.Result {
	out := make([]syncer.Result, len(steps))
	sem := make(chan struct{}, max(jobs, 1))
	var wg sync.WaitGroup
	for i, s := range steps {
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()
			// Don't start new work after Ctrl-C, but let in-flight steps finish
			// (or abort cleanly) rather than killing git mid-merge.
			if err := ctx.Err(); err != nil {
				out[i] = syncer.Result{Step: s, Error: "cancelled"}
				return
			}
			out[i] = syncer.Apply(context.WithoutCancel(ctx), s)
		})
	}
	wg.Wait()
	return out
}

func stdinIsTerminal() bool {
	fi, err := os.Stdin.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

func confirm(prompt string) bool {
	fmt.Print(prompt)
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true
	}
	return false
}
