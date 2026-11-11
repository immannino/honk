// Package filter implements the small expression language behind `honk --filter`.
//
//	updated > 1d                          touched within the last day
//	updated < 2026-01-01                  last commit before that date
//	age > 30d or updated > 2026-06-01     older than 30 days, or after a date
//	(updated > 1w and updated < 2d)       parentheses, "and"/"or" (also &&, ||)
//
// Fields:
//
//	updated  the time of the last commit. The value is a date (2026-01-01,
//	         2026-01-01T15:04) or a duration, meaning that long ago, so
//	         "updated > 1d" reads "updated after one day ago".
//	age      how long ago the last commit was; the value is a duration, so
//	         "age > 1d" reads "older than a day".
//
// Durations are a number plus m, h, d, w, mo or y (mo = 30d, y = 365d).
// Repos with no commits never match.
package filter

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/immannino/honk/internal/git"
)

// Func reports whether a repo status matches.
type Func func(s *git.Status, now time.Time) bool

// Parse compiles expr. An empty expression matches everything.
func Parse(expr string) (Func, error) {
	if strings.TrimSpace(expr) == "" {
		return func(*git.Status, time.Time) bool { return true }, nil
	}
	toks, err := lex(expr)
	if err != nil {
		return nil, err
	}
	p := &parser{toks: toks}
	f, err := p.or()
	if err != nil {
		return nil, err
	}
	if p.pos < len(p.toks) {
		return nil, fmt.Errorf("unexpected %q", p.toks[p.pos])
	}
	return f, nil
}

func lex(s string) ([]string, error) {
	var toks []string
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == ' ' || c == '\t':
			i++
		case c == '(' || c == ')':
			toks = append(toks, string(c))
			i++
		case c == '&' || c == '|':
			if i+1 >= len(s) || s[i+1] != c {
				return nil, fmt.Errorf("unexpected %q (did you mean %q?)", string(c), string(c)+string(c))
			}
			toks = append(toks, s[i:i+2])
			i += 2
		case c == '<' || c == '>' || c == '=' || c == '!':
			j := i + 1
			if j < len(s) && s[j] == '=' {
				j++
			}
			toks = append(toks, s[i:j])
			i = j
		default:
			j := i
			for j < len(s) && !strings.ContainsRune(" \t()&|<>=!", rune(s[j])) {
				j++
			}
			toks = append(toks, s[i:j])
			i = j
		}
	}
	return toks, nil
}

type parser struct {
	toks []string
	pos  int
}

func (p *parser) peek() string {
	if p.pos < len(p.toks) {
		return p.toks[p.pos]
	}
	return ""
}

func isOr(t string) bool  { return t == "||" || strings.EqualFold(t, "or") }
func isAnd(t string) bool { return t == "&&" || strings.EqualFold(t, "and") }

func (p *parser) or() (Func, error) {
	l, err := p.and()
	if err != nil {
		return nil, err
	}
	for isOr(p.peek()) {
		p.pos++
		r, err := p.and()
		if err != nil {
			return nil, err
		}
		prev := l
		l = func(s *git.Status, now time.Time) bool { return prev(s, now) || r(s, now) }
	}
	return l, nil
}

func (p *parser) and() (Func, error) {
	l, err := p.term()
	if err != nil {
		return nil, err
	}
	for isAnd(p.peek()) {
		p.pos++
		r, err := p.term()
		if err != nil {
			return nil, err
		}
		prev := l
		l = func(s *git.Status, now time.Time) bool { return prev(s, now) && r(s, now) }
	}
	return l, nil
}

func (p *parser) term() (Func, error) {
	if p.peek() == "(" {
		p.pos++
		f, err := p.or()
		if err != nil {
			return nil, err
		}
		if p.peek() != ")" {
			return nil, fmt.Errorf("missing closing parenthesis")
		}
		p.pos++
		return f, nil
	}
	if p.pos+3 > len(p.toks) {
		return nil, fmt.Errorf("incomplete comparison, expected FIELD OP VALUE")
	}
	field, op, val := strings.ToLower(p.toks[p.pos]), p.toks[p.pos+1], p.toks[p.pos+2]
	p.pos += 3

	switch op {
	case ">", ">=", "<", "<=", "=", "==", "!=":
	default:
		return nil, fmt.Errorf("unknown operator %q", op)
	}

	switch field {
	case "updated":
		if d, ok := parseDuration(val); ok {
			return func(s *git.Status, now time.Time) bool {
				return s.Last != nil && cmpTime(s.Last.When, now.Add(-d), op)
			}, nil
		}
		t, err := parseDate(val)
		if err != nil {
			return nil, fmt.Errorf("bad value %q for updated: want a duration like 7d or a date like 2026-01-01", val)
		}
		return func(s *git.Status, _ time.Time) bool {
			return s.Last != nil && cmpTime(s.Last.When, t, op)
		}, nil
	case "age":
		d, ok := parseDuration(val)
		if !ok {
			return nil, fmt.Errorf("bad value %q for age: want a duration like 7d", val)
		}
		return func(s *git.Status, now time.Time) bool {
			return s.Last != nil && cmpDur(now.Sub(s.Last.When), d, op)
		}, nil
	}
	return nil, fmt.Errorf("unknown field %q (have: updated, age)", field)
}

func cmpTime(a, b time.Time, op string) bool {
	switch op {
	case ">":
		return a.After(b)
	case ">=":
		return !a.Before(b)
	case "<":
		return a.Before(b)
	case "<=":
		return !a.After(b)
	case "!=":
		return !a.Equal(b)
	}
	return a.Equal(b)
}

func cmpDur(a, b time.Duration, op string) bool {
	switch op {
	case ">":
		return a > b
	case ">=":
		return a >= b
	case "<":
		return a < b
	case "<=":
		return a <= b
	case "!=":
		return a != b
	}
	return a == b
}

func parseDuration(s string) (time.Duration, bool) {
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	if i == 0 || i == len(s) {
		return 0, false
	}
	n, err := strconv.Atoi(s[:i])
	if err != nil {
		return 0, false
	}
	var unit time.Duration
	switch strings.ToLower(s[i:]) {
	case "m", "min":
		unit = time.Minute
	case "h":
		unit = time.Hour
	case "d":
		unit = 24 * time.Hour
	case "w":
		unit = 7 * 24 * time.Hour
	case "mo":
		unit = 30 * 24 * time.Hour
	case "y":
		unit = 365 * 24 * time.Hour
	default:
		return 0, false
	}
	return time.Duration(n) * unit, true
}

func parseDate(s string) (time.Time, error) {
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04", "2006-01-02 15:04", "2006-01-02"} {
		if t, err := time.ParseInLocation(layout, s, time.Local); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("unrecognised date %q", s)
}
