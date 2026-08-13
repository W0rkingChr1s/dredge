// Package systemd renders and installs the units dredge uses for unattended
// runs on a host.
package systemd

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// cronMacros maps the common cron shorthands to their 5-field equivalent.
var cronMacros = map[string]string{
	"@yearly":   "0 0 1 1 *",
	"@annually": "0 0 1 1 *",
	"@monthly":  "0 0 1 * *",
	"@weekly":   "0 0 * * 0",
	"@daily":    "0 0 * * *",
	"@midnight": "0 0 * * *",
	"@hourly":   "0 * * * *",
}

var monthNames = map[string]int{
	"jan": 1, "feb": 2, "mar": 3, "apr": 4, "may": 5, "jun": 6,
	"jul": 7, "aug": 8, "sep": 9, "oct": 10, "nov": 11, "dec": 12,
	"january": 1, "february": 2, "march": 3, "april": 4, "june": 6,
	"july": 7, "august": 8, "september": 9, "october": 10,
	"november": 11, "december": 12,
}

var dowNames = map[string]int{
	"sun": 0, "mon": 1, "tue": 2, "wed": 3, "thu": 4, "fri": 5, "sat": 6,
	"sunday": 0, "monday": 1, "tuesday": 2, "wednesday": 3,
	"thursday": 4, "friday": 5, "saturday": 6,
}

// dowSystemd is indexed by cron day-of-week (0 = Sunday).
var dowSystemd = [7]string{"Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"}

var (
	minuteField = field{"Minute", 0, 59, nil}
	hourField   = field{"Stunde", 0, 23, nil}
	domField    = field{"Tag", 1, 31, nil}
	monthField  = field{"Monat", 1, 12, monthNames}
)

// CronToOnCalendar converts a 5-field cron expression (or an @-macro) into a
// systemd OnCalendar expression. The returned warnings describe semantic
// differences between cron and systemd that survive the translation.
func CronToOnCalendar(expr string) (string, []string, error) {
	e := strings.TrimSpace(expr)
	if e == "" {
		return "", nil, errors.New("leerer Cron-Ausdruck")
	}
	if strings.HasPrefix(e, "@") {
		m, ok := cronMacros[strings.ToLower(e)]
		if !ok {
			return "", nil, fmt.Errorf("Cron-Makro %q wird nicht unterstützt "+
				"(möglich: @hourly @daily @midnight @weekly @monthly @yearly)", e)
		}
		e = m
	}
	f := strings.Fields(e)
	if len(f) != 5 {
		return "", nil, fmt.Errorf("cron braucht 5 Felder "+
			"(Minute Stunde Tag Monat Wochentag), %q hat %d", expr, len(f))
	}

	minute, err := minuteField.convert(f[0])
	if err != nil {
		return "", nil, err
	}
	hour, err := hourField.convert(f[1])
	if err != nil {
		return "", nil, err
	}
	dom, err := domField.convert(f[2])
	if err != nil {
		return "", nil, err
	}
	month, err := monthField.convert(f[3])
	if err != nil {
		return "", nil, err
	}
	dow, err := convertDOW(f[4])
	if err != nil {
		return "", nil, err
	}

	var warns []string
	if dow != "" && dom != "*" {
		warns = append(warns, "Monatstag und Wochentag sind beide gesetzt: cron "+
			"verknüpft sie mit ODER, systemd mit UND – der Timer läuft dadurch "+
			"seltener als der Cron-Ausdruck vermuten lässt")
	}

	cal := fmt.Sprintf("*-%s-%s %s:%s:00", month, dom, hour, minute)
	if dow != "" {
		cal = dow + " " + cal
	}
	return cal, warns, nil
}

// WithTimezone appends a timezone to an OnCalendar expression. An empty or
// system-local timezone is left off, so systemd keeps using the host's zone.
// Requires systemd >= 252 on the target host.
func WithTimezone(onCalendar, tz string) string {
	if !HasExplicitTimezone(tz) {
		return onCalendar
	}
	return onCalendar + " " + strings.TrimSpace(tz)
}

// HasExplicitTimezone reports whether tz names a real zone (as opposed to the
// empty string or one of the "use the system zone" spellings).
func HasExplicitTimezone(tz string) bool {
	switch strings.ToLower(strings.TrimSpace(tz)) {
	case "", "local", "system":
		return false
	}
	return true
}

// field describes one numeric cron field and how to validate it.
type field struct {
	name     string
	min, max int
	names    map[string]int
}

// convert translates a whole cron field (possibly a comma-separated list).
func (f field) convert(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", fmt.Errorf("%s: leeres Feld", f.name)
	}
	if s == "?" {
		s = "*"
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		t, err := f.term(strings.TrimSpace(p))
		if err != nil {
			return "", err
		}
		if t == "*" {
			// A wildcard anywhere in the list covers the whole range.
			return "*", nil
		}
		out = append(out, t)
	}
	return strings.Join(out, ","), nil
}

// term translates a single cron term: "*", "5", "1-5", "*/2", "1-9/2", "5/10".
func (f field) term(p string) (string, error) {
	if p == "" {
		return "", fmt.Errorf("%s: leerer Eintrag", f.name)
	}
	base, step := p, 0
	if i := strings.Index(p, "/"); i >= 0 {
		base = p[:i]
		n, err := strconv.Atoi(strings.TrimSpace(p[i+1:]))
		if err != nil || n < 1 {
			return "", fmt.Errorf("%s: ungültige Schrittweite in %q", f.name, p)
		}
		step = n
	}

	switch {
	case base == "*" || base == "?":
		if step == 0 {
			return "*", nil
		}
		// systemd spells "every N" as "<start>/N", e.g. 00/15 for */15.
		return fmt.Sprintf("%02d/%d", f.min, step), nil

	case strings.Contains(base, "-"):
		ab := strings.SplitN(base, "-", 2)
		a, err := f.value(ab[0])
		if err != nil {
			return "", err
		}
		b, err := f.value(ab[1])
		if err != nil {
			return "", err
		}
		if a > b {
			return "", fmt.Errorf("%s: Bereich %q läuft rückwärts", f.name, base)
		}
		r := fmt.Sprintf("%02d..%02d", a, b)
		if step > 0 {
			r += fmt.Sprintf("/%d", step)
		}
		return r, nil

	default:
		v, err := f.value(base)
		if err != nil {
			return "", err
		}
		if step > 0 {
			return fmt.Sprintf("%02d/%d", v, step), nil
		}
		return fmt.Sprintf("%02d", v), nil
	}
}

// value parses a single number or name and checks it against the field range.
func (f field) value(s string) (int, error) {
	s = strings.TrimSpace(s)
	if f.names != nil {
		if n, ok := f.names[strings.ToLower(s)]; ok {
			return n, nil
		}
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("%s: %q ist keine Zahl", f.name, s)
	}
	if n < f.min || n > f.max {
		return 0, fmt.Errorf("%s: %d liegt außerhalb von %d–%d", f.name, n, f.min, f.max)
	}
	return n, nil
}

// convertDOW translates the day-of-week field into systemd weekday names.
// An unrestricted field yields "" so the caller can omit the weekday part.
func convertDOW(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" || s == "*" || s == "?" {
		return "", nil
	}
	var out []string
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p == "*" || p == "?" {
			return "", nil
		}
		if strings.Contains(p, "/") {
			return "", fmt.Errorf("Wochentag: Schrittweite %q lässt sich nicht nach "+
				"OnCalendar übersetzen – bitte die Tage einzeln aufzählen, z. B. 1,3,5", p)
		}
		if strings.Contains(p, "-") {
			ab := strings.SplitN(p, "-", 2)
			a, err := dowValue(ab[0])
			if err != nil {
				return "", err
			}
			b, err := dowValue(ab[1])
			if err != nil {
				return "", err
			}
			if a == b {
				out = append(out, dowSystemd[a])
				continue
			}
			out = append(out, dowSystemd[a]+".."+dowSystemd[b])
			continue
		}
		n, err := dowValue(p)
		if err != nil {
			return "", err
		}
		out = append(out, dowSystemd[n])
	}
	return strings.Join(out, ","), nil
}

// dowValue parses a cron weekday (0-7 or a name) into 0..6 with 0 = Sunday.
func dowValue(s string) (int, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	if n, ok := dowNames[s]; ok {
		return n, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("Wochentag: %q ist weder Zahl noch Name (0-7, sun..sat)", s)
	}
	if n < 0 || n > 7 {
		return 0, fmt.Errorf("Wochentag: %d liegt außerhalb von 0–7", n)
	}
	if n == 7 {
		n = 0 // both 0 and 7 mean Sunday
	}
	return n, nil
}
