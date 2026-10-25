// Package cron parses standard five-field cron schedules ("*/5 * * * *") and finds their next run times.
//
// Supported: numbers, *, ranges (1-5), steps (*/15, 0-30/10), lists (1,15,30), month and weekday names
// (jan, mon-fri), 7 as Sunday, and the macros @hourly, @daily, @midnight, @weekly, @monthly, @yearly and
// @annually. Like Vixie cron, when both day-of-month and day-of-week are restricted, a day matching either runs.
package cron

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

type Schedule struct {
	minute, hour, dom, month, dow uint64 // bit sets
	domStar, dowStar              bool
	expr                          string
}

var macros = map[string]string{
	"@yearly": "0 0 1 1 *", "@annually": "0 0 1 1 *", "@monthly": "0 0 1 * *", "@weekly": "0 0 * * 0",
	"@daily": "0 0 * * *", "@midnight": "0 0 * * *", "@hourly": "0 * * * *",
}

var monthNames = map[string]int{"jan": 1, "feb": 2, "mar": 3, "apr": 4, "may": 5, "jun": 6, "jul": 7, "aug": 8, "sep": 9, "oct": 10, "nov": 11, "dec": 12}
var dayNames = map[string]int{"sun": 0, "mon": 1, "tue": 2, "wed": 3, "thu": 4, "fri": 5, "sat": 6}

type field struct {
	name     string
	min, max int
	names    map[string]int
}

var fields = [5]field{
	{"minute", 0, 59, nil}, {"hour", 0, 23, nil}, {"day of month", 1, 31, nil}, {"month", 1, 12, monthNames}, {"day of week", 0, 7, dayNames},
}

// Parse reads a schedule. Errors are written for people ("hour must be 0-23, got 24").
func Parse(expr string) (*Schedule, error) {
	expr = strings.Join(strings.Fields(strings.ToLower(expr)), " ")
	spec := expr
	if strings.HasPrefix(expr, "@") {
		m, ok := macros[expr]
		if !ok {
			return nil, fmt.Errorf("unknown shortcut %s; use @hourly, @daily, @weekly, @monthly or @yearly", expr)
		}
		spec = m
	}
	parts := strings.Fields(spec)
	if len(parts) != 5 {
		return nil, fmt.Errorf("a schedule has 5 fields (minute hour day-of-month month day-of-week), this has %d", len(parts))
	}
	s := &Schedule{expr: expr, domStar: parts[2] == "*" || parts[2] == "?", dowStar: parts[4] == "*" || parts[4] == "?"}
	sets := [5]*uint64{&s.minute, &s.hour, &s.dom, &s.month, &s.dow}
	for i, p := range parts {
		bits, err := parseField(p, fields[i])
		if err != nil {
			return nil, err
		}
		*sets[i] = bits
	}
	if s.dow&(1<<7) != 0 {
		s.dow |= 1 // 7 is Sunday too
	}
	// Reject schedules that can never run, like 30 February.
	if s.dowStar && !s.anyDay() {
		return nil, fmt.Errorf("that day of the month never occurs in the chosen months")
	}
	return s, nil
}

// anyDay reports whether some chosen day of month exists in some chosen month (only checked when dow is *).
func (s *Schedule) anyDay() bool {
	days := [13]int{0, 31, 29, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31}
	for m := 1; m <= 12; m++ {
		if s.month&(1<<m) == 0 {
			continue
		}
		for d := 1; d <= days[m]; d++ {
			if s.dom&(1<<d) != 0 {
				return true
			}
		}
	}
	return false
}

func parseField(text string, f field) (uint64, error) {
	var bits uint64
	for _, part := range strings.Split(text, ",") {
		if part == "" {
			return 0, fmt.Errorf("%s has an empty list item", f.name)
		}
		rng, stepText, hasStep := strings.Cut(part, "/")
		step := 1
		if hasStep {
			n, err := strconv.Atoi(stepText)
			if err != nil || n < 1 {
				return 0, fmt.Errorf("%s step must be a positive number, got %q", f.name, stepText)
			}
			step = n
		}
		lo, hi := f.min, f.max
		if f.name == "day of week" {
			hi = 6 // * means Sunday–Saturday once
		}
		if rng != "*" && rng != "?" {
			a, b, isRange := strings.Cut(rng, "-")
			var err error
			if lo, err = value(a, f); err != nil {
				return 0, err
			}
			hi = lo
			if isRange {
				if hi, err = value(b, f); err != nil {
					return 0, err
				}
				if hi < lo {
					return 0, fmt.Errorf("%s range %s goes backwards", f.name, rng)
				}
			} else if hasStep {
				hi = f.max // "5/15" means from 5 to the end, every 15
			}
		}
		for v := lo; v <= hi; v += step {
			bits |= 1 << v
		}
	}
	return bits, nil
}

func value(text string, f field) (int, error) {
	if n, ok := f.names[text]; ok {
		return n, nil
	}
	n, err := strconv.Atoi(text)
	if err != nil {
		return 0, fmt.Errorf("%s: %q isn't a number", f.name, text)
	}
	if n < f.min || n > f.max {
		return 0, fmt.Errorf("%s must be %d-%d, got %d", f.name, f.min, f.max, n)
	}
	return n, nil
}

// Next returns the first run strictly after t, in loc. It returns the zero time if none exists within 5 years.
func (s *Schedule) Next(t time.Time, loc *time.Location) time.Time {
	if loc == nil {
		loc = time.UTC
	}
	t = t.In(loc).Truncate(time.Minute).Add(time.Minute)
	limit := t.AddDate(5, 0, 0)
	for t.Before(limit) {
		if s.month&(1<<int(t.Month())) == 0 {
			t = time.Date(t.Year(), t.Month()+1, 1, 0, 0, 0, 0, loc)
			continue
		}
		if !s.dayMatches(t) {
			t = time.Date(t.Year(), t.Month(), t.Day()+1, 0, 0, 0, 0, loc)
			continue
		}
		if s.hour&(1<<t.Hour()) == 0 {
			next := time.Date(t.Year(), t.Month(), t.Day(), t.Hour()+1, 0, 0, 0, loc)
			if !next.After(t) { // a DST jump that repeats an hour
				next = t.Add(time.Hour).Truncate(time.Hour)
			}
			t = next
			continue
		}
		if s.minute&(1<<t.Minute()) == 0 {
			t = t.Add(time.Minute)
			continue
		}
		return t
	}
	return time.Time{}
}

func (s *Schedule) dayMatches(t time.Time) bool {
	dom := s.dom&(1<<t.Day()) != 0
	dow := s.dow&(1<<int(t.Weekday())) != 0
	switch {
	case s.domStar && s.dowStar:
		return true
	case s.domStar:
		return dow
	case s.dowStar:
		return dom
	}
	return dom || dow
}

// Describe puts common schedules into words and falls back to the expression for unusual ones.
func Describe(expr string) string {
	s, err := Parse(expr)
	if err != nil {
		return expr
	}
	p := strings.Fields(s.spec())
	min, hour, dom, mon, dow := p[0], p[1], p[2], p[3], p[4]
	at := ""
	if isNum(min) && isNum(hour) {
		at = fmt.Sprintf("%02d:%02d", atoi(hour), atoi(min))
	}
	switch {
	case min == "*" && hour == "*" && dom == "*" && mon == "*" && dow == "*":
		return "Every minute"
	case strings.HasPrefix(min, "*/") && hour == "*" && dom == "*" && mon == "*" && dow == "*":
		return "Every " + strings.TrimPrefix(min, "*/") + " minutes"
	case isNum(min) && hour == "*" && dom == "*" && mon == "*" && dow == "*":
		if min == "0" {
			return "Every hour"
		}
		return fmt.Sprintf("Every hour at :%02d", atoi(min))
	case isNum(min) && strings.HasPrefix(hour, "*/") && dom == "*" && mon == "*" && dow == "*":
		return fmt.Sprintf("Every %s hours at :%02d", strings.TrimPrefix(hour, "*/"), atoi(min))
	case at != "" && dom == "*" && mon == "*" && dow == "*":
		return "Every day at " + at
	case at != "" && dom == "*" && mon == "*" && (dow == "1-5" || dow == "mon-fri"):
		return "Weekdays at " + at
	case at != "" && dom == "*" && mon == "*" && isDay(dow):
		return "Every " + dayName(dow) + " at " + at
	case at != "" && isNum(dom) && mon == "*" && dow == "*":
		return fmt.Sprintf("Monthly on day %s at %s", dom, at)
	case at != "" && dom == "1" && mon == "1" && dow == "*":
		return "Every year on 1 January at " + at
	}
	return s.expr
}

func (s *Schedule) spec() string {
	if m, ok := macros[s.expr]; ok {
		return m
	}
	return s.expr
}

func atoi(s string) int { n, _ := strconv.Atoi(s); return n }

func isNum(s string) bool { _, err := strconv.Atoi(s); return err == nil }

func isDay(s string) bool {
	if _, ok := dayNames[s]; ok {
		return true
	}
	n, err := strconv.Atoi(s)
	return err == nil && n >= 0 && n <= 7
}

func dayName(s string) string {
	names := []string{"Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday", "Sunday"}
	if n, ok := dayNames[s]; ok {
		return names[n]
	}
	n, _ := strconv.Atoi(s)
	return names[n]
}
