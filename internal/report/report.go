// Package report renders repo statuses for humans (table) and machines (JSON).
package report

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/immannino/honk/internal/git"
)

// JSON writes statuses as an indented JSON array.
func JSON(w io.Writer, statuses []*git.Status) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if statuses == nil {
		statuses = []*git.Status{}
	}
	return enc.Encode(statuses)
}

// TableOptions tunes the human-readable output.
type TableOptions struct {
	Color      bool
	SubjectLen int // truncate commit subjects to this many runes; 0 means 50
	Now        time.Time
}

// Table writes an aligned, optionally colored table followed by a one-line summary.
func Table(w io.Writer, statuses []*git.Status, opts TableOptions) error {
	if opts.SubjectLen == 0 {
		opts.SubjectLen = 50
	}
	if opts.Now.IsZero() {
		opts.Now = time.Now()
	}
	p := palette{on: opts.Color}

	rows := [][]string{{
		p.bold("REPO"), p.bold("BRANCH"), p.bold("CHANGES"), p.bold("UPSTREAM"),
		p.bold("BASE"), p.bold("REBASE"), p.bold("AGE"), p.bold("LAST COMMIT"),
	}}
	for _, s := range statuses {
		rows = append(rows, row(s, p, opts))
	}

	if err := writeAligned(w, rows); err != nil {
		return err
	}
	_, err := fmt.Fprintln(w, "\n"+summary(statuses, p))
	return err
}

func row(s *git.Status, p palette, opts TableOptions) []string {
	name := p.cyan(s.RelPath)
	if s.Error != "" {
		return []string{name, p.red("error"), "", "", "", "", "", p.red(oneLine(s.Error))}
	}

	branch := s.Branch
	if s.Detached {
		branch = p.yellow("(detached)")
	}
	if s.Operation != "" {
		branch += " " + p.red("["+s.Operation+"]")
	}

	age, last := p.dim("-"), p.dim("(no commits)")
	if s.Last != nil {
		age = relTime(opts.Now.Sub(s.Last.When))
		last = p.dim(s.Last.Hash) + " " + truncate(s.Last.Subject, opts.SubjectLen)
	}

	return []string{name, branch, changes(s, p), upstream(s, p), base(s, p), rebase(s, p), age, last}
}

func changes(s *git.Status, p palette) string {
	var parts []string
	add := func(n int, sym string, color func(string) string) {
		if n > 0 {
			parts = append(parts, color(fmt.Sprintf("%s%d", sym, n)))
		}
	}
	add(s.Conflicted, "!", p.red)
	add(s.Staged, "+", p.green)
	add(s.Unstaged, "~", p.yellow)
	add(s.Untracked, "?", p.dim)
	add(s.Stashes, "*", p.magenta)
	if len(parts) == 0 {
		return p.dim("clean")
	}
	return strings.Join(parts, " ")
}

func upstream(s *git.Status, p palette) string {
	if s.Upstream == "" {
		return p.dim("-")
	}
	out := aheadBehind(s.Ahead, s.Behind, p)
	if s.FetchError != "" {
		out += " " + p.red("(fetch failed)")
	}
	return out
}

func base(s *git.Status, p palette) string {
	if s.Base == "" {
		return p.dim("-")
	}
	return p.dim(s.Base) + " " + aheadBehind(s.BaseAhead, s.BaseBehind, p)
}

func aheadBehind(ahead, behind int, p palette) string {
	if ahead == 0 && behind == 0 {
		return p.green("=")
	}
	var parts []string
	if ahead > 0 {
		parts = append(parts, p.cyan(fmt.Sprintf("↑%d", ahead)))
	}
	if behind > 0 {
		parts = append(parts, p.yellow(fmt.Sprintf("↓%d", behind)))
	}
	return strings.Join(parts, " ")
}

func rebase(s *git.Status, p palette) string {
	label := string(s.Rebase)
	switch s.Rebase {
	case git.RebaseUpToDate:
		return p.dim(label)
	case git.RebaseReady, git.RebaseFastForward:
		return p.green(label)
	case git.RebaseConflicts:
		return p.red(fmt.Sprintf("%s (%d)", label, len(s.ConflictFiles)))
	case git.RebaseDirty:
		if len(s.ConflictFiles) > 0 {
			label += fmt.Sprintf(", %d conflicts", len(s.ConflictFiles))
		}
		return p.yellow(label)
	case git.RebaseBusy:
		return p.red(label)
	default:
		return p.dim(label)
	}
}

func summary(statuses []*git.Status, p palette) string {
	var dirty, behind, ready, conflicts, errs int
	for _, s := range statuses {
		if s.Error != "" {
			errs++
			continue
		}
		if s.HasChanges() {
			dirty++
		}
		if s.Behind > 0 || s.BaseBehind > 0 {
			behind++
		}
		switch s.Rebase {
		case git.RebaseReady, git.RebaseFastForward:
			ready++
		case git.RebaseConflicts:
			conflicts++
		}
	}

	noun := "repos"
	if len(statuses) == 1 {
		noun = "repo"
	}
	parts := []string{fmt.Sprintf("%d %s", len(statuses), noun)}
	add := func(n int, label string, color func(string) string) {
		if n > 0 {
			parts = append(parts, color(fmt.Sprintf("%d %s", n, label)))
		}
	}
	add(dirty, "with changes", p.yellow)
	add(behind, "behind", p.yellow)
	add(ready, "ready to rebase", p.green)
	add(conflicts, "with conflicts", p.red)
	add(errs, "errored", p.red)

	legend := p.dim("  (!conflict +staged ~modified ?untracked *stash  ↑ahead ↓behind)")
	return strings.Join(parts, " · ") + legend
}

// writeAligned pads columns by their visible width, ignoring ANSI escapes,
// which text/tabwriter can't do.
func writeAligned(w io.Writer, rows [][]string) error {
	var widths []int
	for _, r := range rows {
		for i, cell := range r {
			if i >= len(widths) {
				widths = append(widths, 0)
			}
			widths[i] = max(widths[i], visibleLen(cell))
		}
	}

	var b strings.Builder
	for _, r := range rows {
		for i, cell := range r {
			b.WriteString(cell)
			if i < len(r)-1 {
				b.WriteString(strings.Repeat(" ", widths[i]-visibleLen(cell)+2))
			}
		}
		b.WriteString("\n")
	}
	_, err := io.WriteString(w, b.String())
	return err
}

func visibleLen(s string) int {
	n, inEsc := 0, false
	for _, r := range s {
		switch {
		case r == '\x1b':
			inEsc = true
		case inEsc:
			if r == 'm' {
				inEsc = false
			}
		default:
			n++
		}
	}
	return n
}

func truncate(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n-1]) + "…"
}

func oneLine(s string) string {
	s, _, _ = strings.Cut(strings.TrimSpace(s), "\n")
	return truncate(s, 100)
}

func relTime(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	case d < 365*24*time.Hour:
		return fmt.Sprintf("%dmo", int(d.Hours()/(24*30)))
	default:
		return fmt.Sprintf("%dy", int(d.Hours()/(24*365)))
	}
}

type palette struct{ on bool }

func (p palette) wrap(code, s string) string {
	if !p.on || s == "" {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}

func (p palette) bold(s string) string    { return p.wrap("1", s) }
func (p palette) dim(s string) string     { return p.wrap("2", s) }
func (p palette) red(s string) string     { return p.wrap("31", s) }
func (p palette) green(s string) string   { return p.wrap("32", s) }
func (p palette) yellow(s string) string  { return p.wrap("33", s) }
func (p palette) magenta(s string) string { return p.wrap("35", s) }
func (p palette) cyan(s string) string    { return p.wrap("36", s) }
