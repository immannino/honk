package report

import (
	"fmt"
	"io"
	"strings"

	"github.com/immannino/honk/internal/syncer"
)

// Plan writes the planned sync actions. Up-to-date repos are counted but not listed.
func Plan(w io.Writer, steps []syncer.Step, color bool) error {
	p := palette{on: color}
	rows := [][]string{{p.bold("REPO"), p.bold("BRANCH"), p.bold("ACTION"), p.bold("DETAILS")}}

	var act, skipped, current int
	for _, s := range steps {
		switch {
		case s.Action == syncer.ActionNone:
			current++
			continue
		case s.Action == syncer.ActionSkip:
			skipped++
		default:
			act++
		}
		rows = append(rows, []string{p.cyan(s.Status.RelPath), branchLabel(s), actionLabel(s.Action, p), planDetails(s, p)})
	}

	if len(rows) > 1 {
		if err := writeAligned(w, rows); err != nil {
			return err
		}
		fmt.Fprintln(w)
	}

	parts := []string{p.green(fmt.Sprintf("%d to update", act))}
	if skipped > 0 {
		parts = append(parts, p.yellow(fmt.Sprintf("%d need attention", skipped)))
	}
	parts = append(parts, p.dim(fmt.Sprintf("%d already up to date", current)))
	_, err := fmt.Fprintln(w, strings.Join(parts, " · "))
	return err
}

func planDetails(s syncer.Step, p palette) string {
	if s.Action == syncer.ActionSkip {
		return p.yellow(s.Reason)
	}
	st := s.Status
	parts := []string{fmt.Sprintf("%d commit(s) from %s", st.BaseBehind, st.Base)}
	if s.Action == syncer.ActionPush {
		parts = []string{fmt.Sprintf("%d unpushed commit(s)", st.Ahead)}
	}
	if s.Autostash {
		parts = append(parts, "autostash")
	}
	switch {
	case s.ForcePush:
		parts = append(parts, "then force-push (with lease)")
	case s.Push:
		parts = append(parts, "then push")
	case s.NoUpstream:
		parts = append(parts, p.dim("no upstream, won't push"))
	}
	return strings.Join(parts, ", ")
}

// Results writes the outcome of each applied step.
func Results(w io.Writer, results []syncer.Result, color bool) error {
	p := palette{on: color}
	rows := [][]string{{p.bold("REPO"), p.bold("BRANCH"), p.bold("ACTION"), p.bold("RESULT")}}

	var ok, failed int
	for _, r := range results {
		if r.OK() {
			ok++
		} else {
			failed++
		}
		rows = append(rows, []string{p.cyan(r.Step.Status.RelPath), branchLabel(r.Step), actionLabel(r.Step.Action, p), resultDetails(r, p)})
	}
	if err := writeAligned(w, rows); err != nil {
		return err
	}

	parts := []string{p.green(fmt.Sprintf("%d updated", ok))}
	if failed > 0 {
		parts = append(parts, p.red(fmt.Sprintf("%d failed", failed)))
	}
	_, err := fmt.Fprintln(w, "\n"+strings.Join(parts, " · "))
	return err
}

func resultDetails(r syncer.Result, p palette) string {
	moved := short(r.OldHead) + " → " + short(r.NewHead)
	switch {
	case len(r.Conflicts) > 0:
		return p.red("conflicts in " + summarize(r.Conflicts) + "; aborted, branch untouched")
	case r.Error != "":
		return p.red(oneLine(r.Error))
	case r.PushError != "":
		return p.yellow(moved + " locally; push failed: " + oneLine(r.PushError))
	case r.Pushed && r.Step.Action == syncer.ActionPush:
		return p.green("pushed " + short(r.NewHead))
	case r.Pushed:
		return p.green(moved + ", pushed")
	default:
		return p.green(moved)
	}
}

func branchLabel(s syncer.Step) string {
	if s.Status.Branch == "" {
		return "(detached)"
	}
	return s.Status.Branch
}

func actionLabel(a syncer.Action, p palette) string {
	if a == syncer.ActionSkip {
		return p.yellow(string(a))
	}
	return p.green(string(a))
}

func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

func summarize(files []string) string {
	if len(files) <= 3 {
		return strings.Join(files, ", ")
	}
	return fmt.Sprintf("%s and %d more", strings.Join(files[:3], ", "), len(files)-3)
}
