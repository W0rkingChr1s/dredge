package cmd

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

var timerPrint bool

var timerCmd = &cobra.Command{
	Use:   "install-timer",
	Short: "systemd service + timer aus dem Schedule erzeugen (Host-Betrieb)",
	Long: `Erzeugt eine systemd-Service- und Timer-Unit aus dem konfigurierten
Schedule und installiert sie nach /etc/systemd/system (root nötig).

Mit --print werden die Units nur ausgegeben, nichts geschrieben.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, path, err := loadConfig()
		if err != nil {
			return err
		}
		exe, _ := os.Executable()
		if exe == "" {
			exe = "/usr/local/bin/dredge"
		}
		onCal := CronToOnCalendar(cfg.Schedule.Cron)
		service, timer := systemdUnits(exe, path, cfg.Schedule.Timezone, onCal)

		if timerPrint {
			fmt.Println("# /etc/systemd/system/dredge.service")
			fmt.Println(service)
			fmt.Println("# /etc/systemd/system/dredge.timer")
			fmt.Println(timer)
			fmt.Printf("# OnCalendar aus cron %q -> %q\n", cfg.Schedule.Cron, onCal)
			fmt.Println("# Danach: systemctl daemon-reload && systemctl enable --now dredge.timer")
			return nil
		}
		if os.Geteuid() != 0 {
			return fmt.Errorf("Schreiben nach /etc/systemd/system erfordert root – nutze sudo oder --print")
		}
		if err := os.WriteFile("/etc/systemd/system/dredge.service", []byte(service), 0o644); err != nil {
			return err
		}
		if err := os.WriteFile("/etc/systemd/system/dredge.timer", []byte(timer), 0o644); err != nil {
			return err
		}
		fmt.Println("Units geschrieben. Jetzt aktivieren:")
		fmt.Println("  systemctl daemon-reload && systemctl enable --now dredge.timer")
		return nil
	},
}

func systemdUnits(exe, cfgPath, tz, onCal string) (string, string) {
	service := fmt.Sprintf(`[Unit]
Description=dredge cleanup run
Wants=network-online.target
After=network-online.target

[Service]
Type=oneshot
Environment=DREDGE_CONFIG=%s
ExecStart=%s run
`, cfgPath, exe)

	tzLine := ""
	if tz != "" {
		tzLine = "Persistent=true\n"
	}
	timer := fmt.Sprintf(`[Unit]
Description=dredge schedule

[Timer]
OnCalendar=%s
%sUnit=dredge.service

[Install]
WantedBy=timers.target
`, onCal, tzLine)
	return service, timer
}

// CronToOnCalendar converts a 5-field cron expression to a systemd OnCalendar
// string. It handles the common cases (numbers, *, lists, ranges, steps for
// minute/hour, day-of-week names). Falls back to a daily 09:00 on parse issues.
func CronToOnCalendar(cron string) string {
	f := strings.Fields(cron)
	if len(f) != 5 {
		return "*-*-* 09:00:00"
	}
	minute, hour, dom, month, dow := f[0], f[1], f[2], f[3], f[4]

	dowPart := ""
	if dow != "*" && dow != "?" {
		dowPart = cronDOW(dow) + " "
	}
	mn := passthrough(minute, "0")
	hr := passthrough(hour, "0")
	dd := passthrough(dom, "*")
	mm := passthrough(month, "*")

	return fmt.Sprintf("%s*-%s-%s %s:%s:00", dowPart, mm, dd, hr, mn)
}

// passthrough keeps systemd-compatible field syntax (numbers, *, lists, steps).
func passthrough(field, star string) string {
	if field == "*" || field == "?" {
		return star
	}
	// zero-pad single numbers for readability; leave lists/steps as-is.
	if n, err := strconv.Atoi(field); err == nil {
		return fmt.Sprintf("%02d", n)
	}
	return field
}

var dowNames = map[string]string{
	"0": "Sun", "7": "Sun", "1": "Mon", "2": "Tue",
	"3": "Wed", "4": "Thu", "5": "Fri", "6": "Sat",
	"sun": "Sun", "mon": "Mon", "tue": "Tue", "wed": "Wed",
	"thu": "Thu", "fri": "Fri", "sat": "Sat",
}

func cronDOW(spec string) string {
	var out []string
	for _, part := range strings.Split(spec, ",") {
		if strings.Contains(part, "-") {
			rng := strings.SplitN(part, "-", 2)
			a, b := dowName(rng[0]), dowName(rng[1])
			out = append(out, a+".."+b)
			continue
		}
		out = append(out, dowName(part))
	}
	return strings.Join(out, ",")
}

func dowName(s string) string {
	if n, ok := dowNames[strings.ToLower(strings.TrimSpace(s))]; ok {
		return n
	}
	return "Mon"
}

func init() {
	timerCmd.Flags().BoolVar(&timerPrint, "print", false, "Units nur ausgeben, nicht installieren")
	rootCmd.AddCommand(timerCmd)
}
