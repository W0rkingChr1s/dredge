package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/W0rkingChr1s/dredge/internal/config"
	"github.com/W0rkingChr1s/dredge/internal/systemd"
)

var (
	installPrint      bool
	installEnable     bool
	installExecPath   string
	installUser       string
	installGroup      string
	installUnitDir    string
	installPersistent bool
	installJitter     time.Duration
)

var installCmd = &cobra.Command{
	Use:   "install",
	Short: "Als systemd-Timer auf dem Host einrichten",
	Long: `Erzeugt aus der Konfiguration eine systemd-Service- und Timer-Unit,
schreibt sie nach /etc/systemd/system und aktiviert den Timer.

Der Zeitplan wird aus schedule.cron übersetzt, das Freigabe-Timeout aus
notify.telegram.approval_timeout_minutes – damit systemd einen laufenden
Freigabe-Dialog nicht vorzeitig abbricht.

  dredge install --print     nur anzeigen, nichts schreiben
  dredge install --no-enable schreiben, aber nicht aktivieren
  dredge uninstall           Timer wieder entfernen`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, cfgPath, err := loadConfig()
		if err != nil {
			return err
		}

		opts, warns, err := installOptions(cfg, cfgPath)
		if err != nil {
			return err
		}
		service, timer, renderWarns, err := opts.Render()
		if err != nil {
			return err
		}
		warns = append(warns, renderWarns...)

		if installPrint {
			fmt.Printf("# %s\n%s\n", filepath.Join(opts.UnitDir, opts.ServiceName()), service)
			fmt.Printf("# %s\n%s\n", filepath.Join(opts.UnitDir, opts.TimerName()), timer)
			printWarnings(warns)
			fmt.Println("# Installieren mit: sudo dredge install")
			return nil
		}

		if !systemd.Available() {
			return fmt.Errorf("systemd ist auf diesem Host nicht aktiv – im Container " +
				"übernimmt `dredge daemon` den Zeitplan (siehe docker-compose.yml), " +
				"zum Ansehen der Units: dredge install --print")
		}
		if os.Geteuid() != 0 {
			return fmt.Errorf("Schreiben nach %s erfordert root – nutze `sudo dredge install` "+
				"oder `dredge install --print`", opts.UnitDir)
		}

		if err := ensureStateDir(opts.StateDir, opts.User, opts.Group); err != nil {
			return err
		}

		changed, _, err := opts.Write()
		if err != nil {
			return fmt.Errorf("Units schreiben: %w", err)
		}
		if changed {
			fmt.Printf("✓ %s und %s geschrieben nach %s\n",
				opts.ServiceName(), opts.TimerName(), opts.UnitDir)
		} else {
			fmt.Println("✓ Units sind bereits aktuell")
		}
		printWarnings(warns)

		if !installEnable {
			fmt.Println("\nNoch nicht aktiv. Zum Aktivieren:")
			fmt.Printf("  sudo systemctl daemon-reload && sudo systemctl enable --now %s\n", opts.TimerName())
			return nil
		}

		if err := systemd.Systemctl(os.Stdout, "daemon-reload"); err != nil {
			return err
		}
		if err := systemd.Systemctl(os.Stdout, "enable", "--now", opts.TimerName()); err != nil {
			return err
		}
		fmt.Printf("✓ %s aktiviert\n\n", opts.TimerName())
		_ = systemd.Systemctl(os.Stdout, "list-timers", "--no-pager", opts.TimerName())

		fmt.Println("\nNützlich:")
		fmt.Printf("  systemctl start %s      # Lauf sofort auslösen\n", opts.ServiceName())
		fmt.Printf("  journalctl -u %s -f     # mitlesen\n", opts.ServiceName())
		fmt.Println("  sudo dredge uninstall          # Timer wieder entfernen")
		return nil
	},
}

var uninstallCmd = &cobra.Command{
	Use:   "uninstall",
	Short: "systemd-Timer wieder entfernen",
	Long: `Stoppt und deaktiviert den Timer und löscht die beiden Units.

Konfiguration und Historie bleiben erhalten und werden am Ende aufgelistet.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		opts := systemd.Options{UnitDir: unitDir()}
		if systemd.Available() {
			if os.Geteuid() != 0 {
				return fmt.Errorf("Entfernen aus %s erfordert root – nutze `sudo dredge uninstall`", opts.UnitDir)
			}
			// Best effort: a timer that was never enabled makes these fail.
			_ = systemd.Systemctl(os.Stdout, "disable", "--now", opts.TimerName())
			_ = systemd.Systemctl(os.Stdout, "stop", opts.ServiceName())
		}

		removed, err := opts.Remove()
		if err != nil {
			return err
		}
		if len(removed) == 0 {
			fmt.Printf("Nichts zu tun – keine Units in %s gefunden.\n", opts.UnitDir)
			return nil
		}
		for _, p := range removed {
			fmt.Println("✓ entfernt:", p)
		}
		if systemd.Available() {
			_ = systemd.Systemctl(os.Stdout, "daemon-reload")
			_ = systemd.Systemctl(os.Stdout, "reset-failed")
		}

		fmt.Println("\nErhalten geblieben (bei Bedarf von Hand löschen):")
		if cfg, path, err := loadConfig(); err == nil {
			fmt.Println("  Konfiguration:", path)
			if cfg.Audit.LogFile != "" {
				fmt.Println("  Historie:     ", cfg.Audit.LogFile)
			}
		}
		return nil
	},
}

// installOptions derives the unit options from the config, plus any warnings
// about a setup that will not work as intended.
func installOptions(cfg *config.Config, cfgPath string) (systemd.Options, []string, error) {
	exe, warns, err := resolveExecPath(installExecPath)
	if err != nil {
		return systemd.Options{}, nil, err
	}

	absCfg, err := filepath.Abs(cfgPath)
	if err != nil {
		return systemd.Options{}, nil, err
	}

	stateDir := "/var/lib/dredge"
	if cfg.Audit.LogFile != "" {
		if abs, err := filepath.Abs(cfg.Audit.LogFile); err == nil {
			stateDir = filepath.Dir(abs)
		}
	}

	unixSocket := strings.HasPrefix(cfg.Docker.Host, "unix://")
	if installUser != "" && unixSocket {
		warns = append(warns, fmt.Sprintf(
			"Der Dienst läuft als %q und greift auf %s zu – der Nutzer muss in der "+
				"Gruppe docker sein (die Unit setzt SupplementaryGroups=docker)",
			installUser, cfg.Docker.Host))
	}
	if installUser != "" {
		warns = append(warns, fmt.Sprintf(
			"Konfiguration %s muss für %q lesbar sein: sudo chown %s %s",
			absCfg, installUser, installUser, absCfg))
	}

	warns = append(warns, cfg.Validate()...)

	return systemd.Options{
		Name:            systemd.DefaultName,
		UnitDir:         unitDir(),
		ExecPath:        exe,
		ConfigPath:      absCfg,
		StateDir:        stateDir,
		Cron:            cfg.Schedule.Cron,
		Timezone:        cfg.Schedule.Timezone,
		User:            installUser,
		Group:           installGroup,
		DockerGroup:     unixSocket,
		Timeout:         runTimeout(cfg),
		Persistent:      installPersistent,
		RandomizedDelay: installJitter,
		SystemdVersion:  systemd.Version(),
	}, warns, nil
}

// runTimeout bounds a single run generously: a run waiting for a Telegram
// approval must not be killed by systemd before the approval times out.
func runTimeout(cfg *config.Config) time.Duration {
	base := 30 * time.Minute
	if cfg.Notify.Telegram.Enabled && cfg.Notify.Telegram.ApprovalTimeoutMinutes > 0 {
		base = time.Duration(cfg.Notify.Telegram.ApprovalTimeoutMinutes)*time.Minute + 15*time.Minute
	}
	return base
}

// stablePrefixes are locations a binary is expected to stay put in.
var stablePrefixes = []string{"/usr/local/bin", "/usr/bin", "/usr/local/sbin", "/usr/sbin", "/opt"}

// resolveExecPath decides which binary path lands in ExecStart=.
func resolveExecPath(override string) (string, []string, error) {
	if override != "" {
		abs, err := filepath.Abs(override)
		if err != nil {
			return "", nil, err
		}
		if _, err := os.Stat(abs); err != nil {
			// Kein Abbruch: der Pfad darf auch erst später existieren
			// (Image-Bau, Ausrollen per Konfigurationsmanagement).
			return abs, []string{fmt.Sprintf("%s existiert (noch) nicht – "+
				"der Timer schlägt fehl, solange dort kein Binary liegt", abs)}, nil
		}
		return abs, nil, nil
	}

	exe, err := os.Executable()
	if err != nil {
		return "", nil, fmt.Errorf("eigenen Pfad nicht ermittelbar: %w – bitte --exec-path setzen", err)
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}

	for _, p := range stablePrefixes {
		if strings.HasPrefix(exe, p+"/") {
			return exe, nil, nil
		}
	}
	// Running out of a build directory: the unit would point at a path that
	// may well be gone by the time the timer fires.
	warn := fmt.Sprintf(
		"dredge läuft aus %s – die Unit zeigt dann dauerhaft dorthin.\n"+
			"     Besser vorher installieren:  sudo install -m0755 %s /usr/local/bin/dredge\n"+
			"     und danach:                  sudo /usr/local/bin/dredge install",
		exe, exe)
	return exe, []string{warn}, nil
}

// ensureStateDir creates the directory for the audit log before the hardened
// service (which may not run as root) needs to write into it.
func ensureStateDir(dir, user, group string) error {
	if dir == "" {
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("State-Verzeichnis %s: %w", dir, err)
	}
	if user == "" && group == "" {
		return nil
	}
	owner := user
	if group != "" {
		owner += ":" + group
	}
	fmt.Printf("Hinweis: %s ggf. übereignen:  sudo chown -R %s %s\n", dir, owner, dir)
	return nil
}

func unitDir() string {
	if installUnitDir != "" {
		return installUnitDir
	}
	return systemd.DefaultUnitDir
}

// printWarnings goes to stderr so `dredge install --print > unit` stays clean.
func printWarnings(warns []string) {
	for _, w := range warns {
		fmt.Fprintln(os.Stderr, "⚠  "+w)
	}
}

// timerCmd keeps the pre-0.2 name working.
var timerCmd = &cobra.Command{
	Use:        "install-timer",
	Short:      "Alias für `install`",
	Hidden:     true,
	Deprecated: "bitte `dredge install` benutzen",
	RunE:       installCmd.RunE,
}

func init() {
	installCmd.Flags().BoolVar(&installPrint, "print", false, "Units nur ausgeben, nichts schreiben")
	installCmd.Flags().BoolVar(&installEnable, "enable", true, "Timer nach dem Schreiben aktivieren (--enable=false zum Abschalten)")
	installCmd.Flags().StringVar(&installExecPath, "exec-path", "", "Pfad zum dredge-Binary in der Unit (Standard: der laufende Prozess)")
	installCmd.Flags().StringVar(&installUser, "user", "", "Dienst als dieser Nutzer laufen lassen (Standard: root)")
	installCmd.Flags().StringVar(&installGroup, "group", "", "Gruppe für den Dienst")
	installCmd.Flags().StringVar(&installUnitDir, "unit-dir", systemd.DefaultUnitDir, "Zielverzeichnis für die Units")
	installCmd.Flags().BoolVar(&installPersistent, "persistent", true, "verpasste Läufe nach einem Neustart nachholen")
	installCmd.Flags().DurationVar(&installJitter, "jitter", 0, "zufällige Verzögerung des Starts, z. B. 5m")

	timerCmd.Flags().AddFlagSet(installCmd.Flags())
	uninstallCmd.Flags().StringVar(&installUnitDir, "unit-dir", systemd.DefaultUnitDir, "Verzeichnis der Units")

	rootCmd.AddCommand(installCmd)
	rootCmd.AddCommand(uninstallCmd)
	rootCmd.AddCommand(timerCmd)
}
