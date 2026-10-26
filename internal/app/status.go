package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/urfave/cli/v3"

	"github.com/immannino/honk/internal/git"
	"github.com/immannino/honk/internal/report"
	"github.com/immannino/honk/internal/scan"
)

func statusCommand() *cli.Command {
	return &cli.Command{
		Name:      "status",
		Aliases:   []string{"st", "ls"},
		Usage:     "list every repo under DIR with branch, changes and sync state",
		ArgsUsage: "[DIR]",
		Flags: append([]cli.Flag{
			&cli.BoolFlag{Name: "fetch", Aliases: []string{"f"}, Usage: "run git fetch in each repo first so behind-counts are current"},
			&cli.BoolFlag{Name: "json", Usage: "emit JSON instead of a table"},
			&cli.BoolFlag{Name: "dirty", Usage: "only show repos with uncommitted changes"},
			&cli.BoolFlag{Name: "behind", Usage: "only show repos behind their upstream or base"},
		}, repoFlags()...),
		Action: runStatus,
	}
}

// repoFlags are shared by every command that scans and inspects repos.
func repoFlags() []cli.Flag {
	return []cli.Flag{
		&cli.StringFlag{Name: "base", Aliases: []string{"b"}, Usage: "base branch to compare against (default: <remote>/HEAD, then main/master)"},
		&cli.StringSliceFlag{Name: "only", Aliases: []string{"o"}, Usage: "only include repos whose relative path matches this glob (repeatable)"},
		&cli.IntFlag{Name: "depth", Aliases: []string{"d"}, Value: 5, Usage: "max directory depth to search (0 = unlimited)"},
		&cli.StringSliceFlag{Name: "skip", Value: scan.DefaultSkip, Usage: "directory names to skip"},
		&cli.BoolFlag{Name: "nested", Usage: "keep searching inside repos (finds submodules and repos-within-repos)"},
		&cli.IntFlag{Name: "jobs", Aliases: []string{"j"}, Value: min(runtime.NumCPU()*2, 16), Usage: "repos to process concurrently"},
		&cli.DurationFlag{Name: "fetch-timeout", Value: 30 * time.Second, Usage: "per-repo fetch timeout"},
	}
}

func runStatus(ctx context.Context, cmd *cli.Command) error {
	absRoot, statuses, err := collect(ctx, cmd, cmd.Bool("fetch"))
	if err != nil {
		return err
	}
	statuses = filter(statuses, cmd.Bool("dirty"), cmd.Bool("behind"))

	if cmd.Bool("json") {
		return report.JSON(os.Stdout, statuses)
	}
	if len(statuses) == 0 {
		fmt.Fprintf(os.Stderr, "no matching git repositories under %s\n", absRoot)
		return nil
	}
	return report.Table(os.Stdout, statuses, report.TableOptions{Color: useColor(cmd)})
}

// collect scans the DIR argument for repos and inspects each one, returning
// statuses sorted by relative path.
func collect(ctx context.Context, cmd *cli.Command, fetch bool) (string, []*git.Status, error) {
	if cmd.Args().Len() > 1 {
		return "", nil, fmt.Errorf("expected at most one directory, got %d", cmd.Args().Len())
	}
	root := cmd.Args().First()
	if root == "" {
		root = "."
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", nil, err
	}

	paths, err := scan.Find(absRoot, scan.Options{
		MaxDepth: cmd.Int("depth"),
		Skip:     cmd.StringSlice("skip"),
		Nested:   cmd.Bool("nested"),
	})
	if err != nil {
		return "", nil, err
	}

	globs := cmd.StringSlice("only")
	for _, g := range globs {
		if _, err := filepath.Match(g, ""); err != nil {
			return "", nil, fmt.Errorf("bad --only pattern %q: %w", g, err)
		}
	}
	paths = slices.DeleteFunc(paths, func(p string) bool {
		return !matchesAny(globs, relPath(absRoot, p))
	})

	statuses := inspectAll(ctx, paths, cmd.Int("jobs"), git.InspectOptions{
		Fetch:        fetch,
		FetchTimeout: cmd.Duration("fetch-timeout"),
		Base:         cmd.String("base"),
	})
	for _, s := range statuses {
		s.RelPath = relPath(absRoot, s.Path)
	}
	slices.SortFunc(statuses, func(a, b *git.Status) int { return strings.Compare(a.RelPath, b.RelPath) })
	return absRoot, statuses, ctx.Err()
}

func matchesAny(globs []string, rel string) bool {
	if len(globs) == 0 {
		return true
	}
	for _, g := range globs {
		if ok, _ := filepath.Match(g, rel); ok {
			return true
		}
	}
	return false
}

// inspectAll inspects repos with bounded concurrency, drawing a progress
// counter on stderr when it's a terminal.
func inspectAll(ctx context.Context, paths []string, jobs int, opts git.InspectOptions) []*git.Status {
	jobs = max(jobs, 1)
	out := make([]*git.Status, len(paths))
	sem := make(chan struct{}, jobs)

	var done atomic.Int32
	progress := newProgress(len(paths), opts.Fetch)
	defer progress.clear()

	var wg sync.WaitGroup
	for i, p := range paths {
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()
			out[i] = git.Repo{Path: p}.Inspect(ctx, opts)
			progress.update(int(done.Add(1)))
		})
	}
	wg.Wait()
	return out
}

func filter(in []*git.Status, dirty, behind bool) []*git.Status {
	if !dirty && !behind {
		return in
	}
	return slices.DeleteFunc(in, func(s *git.Status) bool {
		if dirty && !s.HasChanges() {
			return true
		}
		if behind && s.Behind == 0 && s.BaseBehind == 0 {
			return true
		}
		return false
	})
}

func relPath(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return path
	}
	return rel
}

type progress struct {
	mu    sync.Mutex
	total int
	verb  string
	tty   bool
}

func newProgress(total int, fetching bool) *progress {
	fi, err := os.Stderr.Stat()
	verb := "inspecting"
	if fetching {
		verb = "fetching"
	}
	// Only worth drawing when it'll take a noticeable moment.
	tty := err == nil && fi.Mode()&os.ModeCharDevice != 0 && (fetching || total > 20)
	return &progress{total: total, verb: verb, tty: tty}
}

func (p *progress) update(n int) {
	if !p.tty {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	fmt.Fprintf(os.Stderr, "\r\x1b[K%s %d/%d…", p.verb, n, p.total)
}

func (p *progress) clear() {
	if p.tty {
		fmt.Fprint(os.Stderr, "\r\x1b[K")
	}
}
