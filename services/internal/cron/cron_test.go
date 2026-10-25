package cron

import (
	"strings"
	"testing"
	"time"
)

func TestNext(t *testing.T) {
	base := time.Date(2026, 10, 25, 14, 7, 30, 0, time.UTC) // a Sunday
	cases := []struct{ expr, want string }{
		{"* * * * *", "2026-10-25 14:08"},
		{"*/15 * * * *", "2026-10-25 14:15"},
		{"0 * * * *", "2026-10-25 15:00"},
		{"0 3 * * *", "2026-10-26 03:00"},
		{"30 9 * * mon-fri", "2026-10-26 09:30"},
		{"0 0 * * 7", "2026-11-01 00:00"}, // 7 is Sunday
		{"0 0 1 * *", "2026-11-01 00:00"},
		{"0 0 29 2 *", "2028-02-29 00:00"},
		{"@weekly", "2026-11-01 00:00"},
		{"0 12 13 * 5", "2026-10-30 12:00"}, // the 13th or any Friday, whichever is first
		{"5/20 * * * *", "2026-10-25 14:25"},
		{"0 9-17/4 * * *", "2026-10-25 17:00"},
		{"0 0 1 jan,jul *", "2027-01-01 00:00"},
	}
	for _, c := range cases {
		s, err := Parse(c.expr)
		if err != nil {
			t.Fatalf("%s: %v", c.expr, err)
		}
		if got := s.Next(base, time.UTC).Format("2006-01-02 15:04"); got != c.want {
			t.Errorf("%s: next = %s, want %s", c.expr, got, c.want)
		}
	}
}

func TestNextInTimezone(t *testing.T) {
	loc, err := time.LoadLocation("Europe/Amsterdam")
	if err != nil {
		t.Skip("no tzdata")
	}
	s, _ := Parse("30 2 * * *")
	// 25 Oct 2026 is the end of summer time in Europe: 02:30 happens twice, but the task runs once.
	got := s.Next(time.Date(2026, 10, 24, 12, 0, 0, 0, loc), loc)
	if got.Format("2006-01-02 15:04") != "2026-10-25 02:30" {
		t.Errorf("got %s", got)
	}
	if after := s.Next(got, loc); after.Format("2006-01-02 15:04") != "2026-10-26 02:30" {
		t.Errorf("ran twice on the night the clocks go back: then %s", after)
	}
	// 29 Mar 2026 skips 02:00-03:00: the 02:30 run moves to the next day that has one.
	got = s.Next(time.Date(2026, 3, 28, 12, 0, 0, 0, loc), loc)
	if got.Day() == 29 && got.Hour() == 2 {
		t.Errorf("ran at a time that doesn't exist: %s", got)
	}
}

func TestParseErrors(t *testing.T) {
	cases := map[string]string{
		"* * * *":      "5 fields",
		"60 * * * *":   "minute must be 0-59",
		"* 24 * * *":   "hour must be 0-23",
		"*/0 * * * *":  "step must be a positive",
		"5-1 * * * *":  "goes backwards",
		"0 0 30 2 *":   "never occurs",
		"@every5m":     "unknown shortcut",
		"0 0 * * fun":  "isn't a number",
		"1,,2 * * * *": "empty list item",
	}
	for expr, want := range cases {
		_, err := Parse(expr)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: got %v, want an error about %q", expr, err, want)
		}
	}
}

func TestDescribe(t *testing.T) {
	cases := map[string]string{
		"*/5 * * * *":  "Every 5 minutes",
		"0 * * * *":    "Every hour",
		"15 * * * *":   "Every hour at :15",
		"0 3 * * *":    "Every day at 03:00",
		"30 9 * * 1-5": "Weekdays at 09:30",
		"0 3 * * 1":    "Every Monday at 03:00",
		"0 4 1 * *":    "Monthly on day 1 at 04:00",
		"@daily":       "Every day at 00:00",
		"0 */6 * * *":  "Every 6 hours at :00",
		"7 1 2-9 * 3":  "7 1 2-9 * 3",
	}
	for expr, want := range cases {
		if got := Describe(expr); got != want {
			t.Errorf("%s: %q, want %q", expr, got, want)
		}
	}
}
