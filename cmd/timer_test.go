package cmd

import "testing"

func TestCronToOnCalendar(t *testing.T) {
	cases := map[string]string{
		"0 9 * * 1":   "Mon *-*-* 09:00:00",
		"0 3 * * *":   "*-*-* 03:00:00",
		"30 2 * * 0":  "Sun *-*-* 02:30:00",
		"0 */6 * * *": "*-*-* */6:00:00",
		"15 4 1 * *":  "*-*-01 04:15:00",
		"0 9 * * 1-5": "Mon..Fri *-*-* 09:00:00",
		"bogus":       "*-*-* 09:00:00",
	}
	for in, want := range cases {
		if got := CronToOnCalendar(in); got != want {
			t.Errorf("CronToOnCalendar(%q) = %q, want %q", in, got, want)
		}
	}
}
