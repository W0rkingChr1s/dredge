package systemd

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// DefaultUnitDir is where system-wide units live on every supported distro.
const DefaultUnitDir = "/etc/systemd/system"

// DefaultName is the base name of the generated units.
const DefaultName = "dredge"

// tzSuffixMinVersion is the first systemd release that understands a timezone
// suffix in OnCalendar= expressions.
const tzSuffixMinVersion = 252

// Options describes the units to render.
type Options struct {
	// Name is the unit base name; "dredge" yields dredge.service/dredge.timer.
	Name string
	// UnitDir is the directory the units are written to.
	UnitDir string
	// ExecPath is the absolute path of the dredge binary.
	ExecPath string
	// ConfigPath is passed to the service as DREDGE_CONFIG.
	ConfigPath string
	// StateDir is made writable for the (otherwise read-only) service.
	StateDir string
	// Cron is the 5-field schedule from the config.
	Cron string
	// Timezone is the schedule's timezone; empty or "Local" uses the host zone.
	Timezone string
	// User/Group run the service as an unprivileged account (optional).
	User, Group string
	// DockerGroup adds the docker group when running as a non-root user and
	// talking to a unix socket.
	DockerGroup bool
	// Timeout bounds a single run. It must exceed the approval timeout,
	// otherwise systemd kills the run while it waits for a Telegram answer.
	Timeout time.Duration
	// Persistent catches up a run that was missed while the host was off.
	Persistent bool
	// RandomizedDelay spreads the start time over the given window.
	RandomizedDelay time.Duration
	// SystemdVersion of the target host; 0 means "unknown, assume recent".
	SystemdVersion int
}

// ServiceName returns the service unit's file name.
func (o Options) ServiceName() string { return o.name() + ".service" }

// TimerName returns the timer unit's file name.
func (o Options) TimerName() string { return o.name() + ".timer" }

func (o Options) name() string {
	if o.Name == "" {
		return DefaultName
	}
	return o.Name
}

func (o Options) unitDir() string {
	if o.UnitDir == "" {
		return DefaultUnitDir
	}
	return o.UnitDir
}

// Render builds the service and timer units. Warnings describe translation
// caveats the user should know about; they are not errors.
func (o Options) Render() (service, timer string, warnings []string, err error) {
	if o.ExecPath == "" {
		return "", "", nil, fmt.Errorf("kein Pfad zum dredge-Binary bekannt")
	}
	onCal, warnings, err := CronToOnCalendar(o.Cron)
	if err != nil {
		return "", "", nil, fmt.Errorf("schedule.cron %q: %w", o.Cron, err)
	}
	if HasExplicitTimezone(o.Timezone) {
		if o.SystemdVersion > 0 && o.SystemdVersion < tzSuffixMinVersion {
			warnings = append(warnings, fmt.Sprintf(
				"systemd %d kann keine Zeitzone in OnCalendar (erst ab %d) – der Timer "+
					"nutzt die Zeitzone des Hosts, nicht %q",
				o.SystemdVersion, tzSuffixMinVersion, o.Timezone))
		} else {
			onCal = WithTimezone(onCal, o.Timezone)
		}
	}
	return o.renderService(), o.renderTimer(onCal), warnings, nil
}

func (o Options) renderService() string {
	var b strings.Builder
	p := func(format string, a ...any) { fmt.Fprintf(&b, format+"\n", a...) }

	p("[Unit]")
	p("Description=dredge – sicheres Aufräumen für Docker")
	p("Documentation=https://github.com/W0rkingChr1s/dredge")
	p("Wants=network-online.target")
	p("After=network-online.target docker.service")
	p("")
	p("[Service]")
	p("Type=oneshot")
	if o.ConfigPath != "" {
		p("Environment=%q", "DREDGE_CONFIG="+o.ConfigPath)
	}
	p("ExecStart=%q run", o.ExecPath)
	p("SyslogIdentifier=%s", o.name())
	p("TimeoutStartSec=%s", o.timeout())
	if o.User != "" {
		p("User=%s", o.User)
	}
	if o.Group != "" {
		p("Group=%s", o.Group)
	}
	if o.User != "" && o.DockerGroup {
		p("SupplementaryGroups=docker")
	}
	p("")
	p("# Härtung – dredge braucht nur Netz/Socket und sein State-Verzeichnis.")
	p("NoNewPrivileges=true")
	p("PrivateTmp=true")
	p("ProtectHome=true")
	p("ProtectSystem=strict")
	if o.StateDir != "" {
		p("ReadWritePaths=%q", o.StateDir)
	}
	p("ProtectKernelTunables=true")
	p("ProtectKernelModules=true")
	p("ProtectControlGroups=true")
	p("RestrictNamespaces=true")
	p("RestrictRealtime=true")
	p("RestrictSUIDSGID=true")
	p("RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6")
	p("LockPersonality=true")
	p("SystemCallFilter=@system-service")
	p("SystemCallErrorNumber=EPERM")
	return b.String()
}

func (o Options) renderTimer(onCalendar string) string {
	var b strings.Builder
	p := func(format string, a ...any) { fmt.Fprintf(&b, format+"\n", a...) }

	p("[Unit]")
	p("Description=dredge Zeitplan (cron: %s)", strings.Join(strings.Fields(o.Cron), " "))
	p("Documentation=https://github.com/W0rkingChr1s/dredge")
	p("")
	p("[Timer]")
	p("OnCalendar=%s", onCalendar)
	if o.Persistent {
		p("Persistent=true")
	}
	if o.RandomizedDelay > 0 {
		p("RandomizedDelaySec=%s", durationSpec(o.RandomizedDelay))
	}
	p("AccuracySec=1min")
	p("Unit=%s", o.ServiceName())
	p("")
	p("[Install]")
	p("WantedBy=timers.target")
	return b.String()
}

func (o Options) timeout() string {
	if o.Timeout <= 0 {
		return "30min"
	}
	return durationSpec(o.Timeout)
}

// durationSpec formats a duration the way systemd spells time spans.
func durationSpec(d time.Duration) string {
	if d%time.Hour == 0 {
		return strconv.Itoa(int(d/time.Hour)) + "h"
	}
	if d%time.Minute == 0 {
		return strconv.Itoa(int(d/time.Minute)) + "min"
	}
	return strconv.Itoa(int(d/time.Second)) + "s"
}

// Write renders and writes both units. It reports whether anything on disk
// actually changed, so callers can skip a needless daemon-reload.
func (o Options) Write() (changed bool, warnings []string, err error) {
	service, timer, warnings, err := o.Render()
	if err != nil {
		return false, warnings, err
	}
	dir := o.unitDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return false, warnings, err
	}
	sChanged, err := writeIfChanged(filepath.Join(dir, o.ServiceName()), service)
	if err != nil {
		return false, warnings, err
	}
	tChanged, err := writeIfChanged(filepath.Join(dir, o.TimerName()), timer)
	if err != nil {
		return sChanged, warnings, err
	}
	return sChanged || tChanged, warnings, nil
}

// Remove deletes the units. It reports whether anything was there to delete.
func (o Options) Remove() (removed []string, err error) {
	dir := o.unitDir()
	for _, n := range []string{o.TimerName(), o.ServiceName()} {
		p := filepath.Join(dir, n)
		err := os.Remove(p)
		switch {
		case err == nil:
			removed = append(removed, p)
		case os.IsNotExist(err):
		default:
			return removed, err
		}
	}
	return removed, nil
}

// writeIfChanged writes content atomically unless the file already matches.
func writeIfChanged(path, content string) (bool, error) {
	if old, err := os.ReadFile(path); err == nil && string(old) == content {
		return false, nil
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(content), 0o644); err != nil {
		return false, err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return false, err
	}
	return true, nil
}

// Available reports whether this host is actually running systemd.
func Available() bool {
	if _, err := exec.LookPath("systemctl"); err != nil {
		return false
	}
	_, err := os.Stat("/run/systemd/system")
	return err == nil
}

var versionRe = regexp.MustCompile(`\b(\d+)`)

// Version returns the running systemd's major version, or 0 if unknown.
func Version() int {
	out, err := exec.Command("systemctl", "--version").Output()
	if err != nil {
		return 0
	}
	line, _, _ := strings.Cut(string(out), "\n")
	m := versionRe.FindStringSubmatch(strings.TrimPrefix(line, "systemd"))
	if m == nil {
		return 0
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return 0
	}
	return n
}

// Systemctl runs systemctl with the given arguments, streaming output to w.
func Systemctl(w io.Writer, args ...string) error {
	cmd := exec.Command("systemctl", args...)
	cmd.Stdout = w
	cmd.Stderr = w
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("systemctl %s: %w", strings.Join(args, " "), err)
	}
	return nil
}
