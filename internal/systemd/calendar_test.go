package systemd

import (
	"os/exec"
	"strings"
	"testing"
)

// TestOnCalendarAcceptedBySystemd prüft die Übersetzung gegen den echten
// Parser. Ohne systemd-analyze (z. B. im Container) wird übersprungen.
func TestOnCalendarAcceptedBySystemd(t *testing.T) {
	bin, err := exec.LookPath("systemd-analyze")
	if err != nil {
		t.Skip("systemd-analyze nicht verfügbar")
	}
	crons := []string{
		"0 9 * * 1", "0 3 * * *", "0 */6 * * *", "30 * * * *",
		"*/15 * * * *", "* * * * *", "0 8-18/2 * * *", "15 4 1 * *",
		"0 9 * * 1-5", "0 5 1,15 * *", "0 5 1 jan *", "@daily",
	}
	for _, cron := range crons {
		cal, _, err := CronToOnCalendar(cron)
		if err != nil {
			t.Errorf("CronToOnCalendar(%q): %v", cron, err)
			continue
		}
		for _, spec := range []string{cal, WithTimezone(cal, "Europe/Berlin")} {
			if out, err := exec.Command(bin, "calendar", spec).CombinedOutput(); err != nil {
				t.Errorf("systemd lehnt %q ab (aus cron %q): %s", spec, cron, out)
			}
		}
	}
}

func TestCronToOnCalendar(t *testing.T) {
	cases := map[string]string{
		// Presets aus dem Setup-Assistenten.
		"0 9 * * 1":   "Mon *-*-* 09:00:00",
		"0 3 * * *":   "*-*-* 03:00:00",
		"0 */6 * * *": "*-*-* 00/6:00:00",

		// Stündliche und minütliche Pläne dürfen nicht zu täglichen werden.
		"30 * * * *":     "*-*-* *:30:00",
		"*/15 * * * *":   "*-*-* *:00/15:00",
		"* * * * *":      "*-*-* *:*:00",
		"0 8-18 * * *":   "*-*-* 08..18:00:00",
		"0 8-18/2 * * *": "*-*-* 08..18/2:00:00",

		// Tage, Monate, Wochentage.
		"15 4 1 * *":      "*-*-01 04:15:00",
		"0 9 * * 1-5":     "Mon..Fri *-*-* 09:00:00",
		"0 9 * * 0":       "Sun *-*-* 09:00:00",
		"0 9 * * 7":       "Sun *-*-* 09:00:00",
		"0 9 * * mon,fri": "Mon,Fri *-*-* 09:00:00",
		"0 5 1 jan *":     "*-01-01 05:00:00",
		"0 5 1,15 * *":    "*-*-01,15 05:00:00",

		// Makros.
		"@daily":  "*-*-* 00:00:00",
		"@hourly": "*-*-* *:00:00",
		"@weekly": "Sun *-*-* 00:00:00",
	}
	for in, want := range cases {
		got, _, err := CronToOnCalendar(in)
		if err != nil {
			t.Errorf("CronToOnCalendar(%q) = Fehler %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("CronToOnCalendar(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCronToOnCalendarErrors(t *testing.T) {
	for _, in := range []string{
		"",
		"bogus",
		"0 9 * *",         // zu wenig Felder
		"0 9 * * * *",     // zu viele Felder
		"0 99 * * *",      // Stunde außerhalb
		"0 9 * * 9",       // Wochentag außerhalb
		"0 9 * * 1-5/2",   // Schrittweite bei Wochentagen
		"0 9 * * montag2", // Unsinn
		"@reboot",
		"0 9 * * abc",
	} {
		if got, _, err := CronToOnCalendar(in); err == nil {
			t.Errorf("CronToOnCalendar(%q) = %q, want Fehler", in, got)
		}
	}
}

func TestCronToOnCalendarWarnsOnDomAndDow(t *testing.T) {
	_, warns, err := CronToOnCalendar("0 9 1 * 1")
	if err != nil {
		t.Fatalf("unerwarteter Fehler: %v", err)
	}
	if len(warns) == 0 {
		t.Fatal("Monatstag + Wochentag sollte eine Warnung erzeugen (cron ODER vs. systemd UND)")
	}
	if !strings.Contains(warns[0], "ODER") {
		t.Errorf("Warnung erklärt den Unterschied nicht: %q", warns[0])
	}
}

func TestWithTimezone(t *testing.T) {
	cases := []struct {
		tz, want string
	}{
		{"Europe/Berlin", "*-*-* 03:00:00 Europe/Berlin"},
		{"UTC", "*-*-* 03:00:00 UTC"},
		{"", "*-*-* 03:00:00"},
		{"Local", "*-*-* 03:00:00"},
		{"local", "*-*-* 03:00:00"},
	}
	for _, c := range cases {
		if got := WithTimezone("*-*-* 03:00:00", c.tz); got != c.want {
			t.Errorf("WithTimezone(_, %q) = %q, want %q", c.tz, got, c.want)
		}
	}
}
