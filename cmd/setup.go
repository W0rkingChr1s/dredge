package cmd

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"

	"github.com/W0rkingChr1s/dredge/internal/config"
	"github.com/W0rkingChr1s/dredge/internal/docker"
	"github.com/W0rkingChr1s/dredge/internal/systemd"
)

var setupCmd = &cobra.Command{
	Use:   "setup",
	Short: "Geführte Ersteinrichtung (interaktiver Assistent)",
	RunE: func(cmd *cobra.Command, args []string) error {
		path := configFile()
		cfg, err := config.Load(path)
		if err != nil {
			cfg = config.Default()
		}
		return runWizard(cfg, path, true)
	},
}

var settingsCmd = &cobra.Command{
	Use:     "settings",
	Aliases: []string{"tui", "config-edit"},
	Short:   "Einstellungen im Terminal-UI ändern",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, path, err := loadConfig()
		if err != nil {
			return err
		}
		return runWizard(cfg, path, false)
	},
}

func init() {
	rootCmd.AddCommand(setupCmd)
	rootCmd.AddCommand(settingsCmd)
}

// runWizard drives the huh form, then validates, tests the connection and saves.
func runWizard(cfg *config.Config, path string, firstRun bool) error {
	// --- bind vars ---
	dockerHost := cfg.Docker.Host
	preset, cronCustom := presetFromCron(cfg.Schedule.Cron)
	tz := cfg.Schedule.Timezone

	mDangling := string(modeOr(cfg.Resources.ImagesDangling))
	mUnused := string(modeOr(cfg.Resources.ImagesUnused))
	mContainers := string(modeOr(cfg.Resources.Containers))
	mNetworks := string(modeOr(cfg.Resources.Networks))
	mBuild := string(modeOr(cfg.Resources.BuildCache))
	mVolumes := string(modeOr(cfg.Resources.Volumes))

	minAge := strconv.Itoa(cfg.Safety.MinAgeHours)
	dryRun := cfg.Safety.DryRun
	protectLabels := strings.Join(cfg.Safety.ProtectLabels, ", ")
	protectVols := strings.Join(cfg.Safety.ProtectVolumeNames, ", ")

	telegramOn := cfg.Notify.Telegram.Enabled
	botToken := cfg.Notify.Telegram.BotToken
	chatID := cfg.Notify.Telegram.ChatID
	approvalTimeout := strconv.Itoa(orInt(cfg.Notify.Telegram.ApprovalTimeoutMinutes, 120))
	onTimeout := orStr(cfg.Notify.Telegram.OnTimeout, "none")
	shoutrrrURLs := strings.Join(cfg.Notify.URLs, "\n")

	saveConfirm := true

	modeOpts := func() []huh.Option[string] {
		return []huh.Option[string]{
			huh.NewOption("aus – nie anfassen", "off"),
			huh.NewOption("auto – automatisch bereinigen", "auto"),
			huh.NewOption("ask – vorher per Telegram fragen", "ask"),
		}
	}

	intro := "Willkommen bei dredge"
	if !firstRun {
		intro = "Einstellungen ändern"
	}

	form := huh.NewForm(
		huh.NewGroup(
			huh.NewNote().Title(intro).Description(
				"Dieser Assistent richtet das sichere Aufräumen deines Docker-Hosts ein.\n"+
					"Mit Pfeiltasten/Tab navigieren, Enter bestätigt."),
		),
		// --- Docker connection ---
		huh.NewGroup(
			huh.NewInput().
				Title("Docker-Host (Engine-API / socket-proxy)").
				Description("z. B. unix:///var/run/docker.sock oder http://dockerproxy:2375").
				Value(&dockerHost),
		).Title("Verbindung"),
		// --- Schedule ---
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("Wie oft soll aufgeräumt werden?").
				Options(
					huh.NewOption("Wöchentlich – Montag 09:00", "weekly"),
					huh.NewOption("Täglich – 03:00", "daily"),
					huh.NewOption("Alle 6 Stunden", "6h"),
					huh.NewOption("Eigener Cron-Ausdruck", "custom"),
				).Value(&preset),
			huh.NewInput().Title("Zeitzone").Value(&tz),
		).Title("Zeitplan"),
		huh.NewGroup(
			huh.NewInput().
				Title("Cron-Ausdruck (5 Felder)").
				Description("Minute Stunde Tag Monat Wochentag – z. B. 0 9 * * 1").
				Value(&cronCustom).
				Validate(validateCronMaybe),
		).Title("Eigener Zeitplan").WithHideFunc(func() bool { return preset != "custom" }),
		// --- Resources ---
		huh.NewGroup(
			huh.NewSelect[string]().Title("Dangling Images").Options(modeOpts()...).Value(&mDangling),
			huh.NewSelect[string]().Title("Ungenutzte Images (nicht dangling)").Options(modeOpts()...).Value(&mUnused),
			huh.NewSelect[string]().Title("Gestoppte Container").Options(modeOpts()...).Value(&mContainers),
			huh.NewSelect[string]().Title("Ungenutzte Netzwerke").Options(modeOpts()...).Value(&mNetworks),
			huh.NewSelect[string]().Title("Build-Cache").Options(modeOpts()...).Value(&mBuild),
			huh.NewSelect[string]().Title("Verwaiste Volumes ⚠ Datenverlust-Risiko").Options(modeOpts()...).Value(&mVolumes),
		).Title("Was bereinigen? (auto / ask / aus)"),
		// --- Safety ---
		huh.NewGroup(
			huh.NewInput().
				Title("Mindestalter in Stunden").
				Description("Nur Objekte entfernen, die älter sind (0 = aus).").
				Value(&minAge).Validate(validateInt),
			huh.NewInput().
				Title("Schutz-Labels (Komma-getrennt)").
				Description("Objekte mit diesen Labels werden nie entfernt, z. B. janitor.keep=true").
				Value(&protectLabels),
			huh.NewInput().
				Title("Geschützte Volume-Namen (Glob, Komma-getrennt)").
				Description("z. B. *_data, portainer_*").
				Value(&protectVols),
			huh.NewConfirm().
				Title("Trockenlauf (nichts wirklich löschen)?").
				Value(&dryRun),
		).Title("Sicherheit"),
		// --- Telegram ---
		huh.NewGroup(
			huh.NewConfirm().Title("Telegram für Freigaben & Meldungen nutzen?").Value(&telegramOn),
		).Title("Telegram (interaktiv)"),
		huh.NewGroup(
			huh.NewInput().Title("Bot-Token").
				Description("Von @BotFather. Eigener Bot empfohlen – Webhook und getUpdates-Polling vertragen sich nicht auf demselben Token.").
				Value(&botToken).EchoMode(huh.EchoModePassword),
			huh.NewInput().Title("Chat-/Gruppen-ID").
				Description("z. B. -1001234567890").
				Value(&chatID),
			huh.NewInput().Title("Freigabe-Timeout (Minuten)").
				Value(&approvalTimeout).Validate(validateInt),
			huh.NewSelect[string]().Title("Aktion bei Timeout").
				Options(
					huh.NewOption("nichts bereinigen", "none"),
					huh.NewOption("nur Dangling Images", "dangling"),
					huh.NewOption("alles bereinigen", "all"),
				).Value(&onTimeout),
		).Title("Telegram-Zugang").WithHideFunc(func() bool { return !telegramOn }),
		// --- Extra channels ---
		huh.NewGroup(
			huh.NewText().
				Title("Weitere Kanäle (shoutrrr-URLs, eine pro Zeile)").
				Description("Mail/ntfy/Gotify/Discord …\n"+
					"z. B. smtp://user:pass@host:587/?from=a@b&to=c@d\n"+
					"      ntfy://ntfy.sh/mein-topic").
				Value(&shoutrrrURLs).Lines(4),
		).Title("Zusätzliche Benachrichtigungen (optional)"),
		// --- Save ---
		huh.NewGroup(
			huh.NewConfirm().Title("Konfiguration speichern?").Value(&saveConfirm),
		),
	).WithTheme(huh.ThemeCharm())

	if err := form.Run(); err != nil {
		return err
	}
	if !saveConfirm {
		fmt.Println("Abgebrochen – nichts gespeichert.")
		return nil
	}

	// --- map back to config ---
	cfg.Docker.Host = strings.TrimSpace(dockerHost)
	cfg.Schedule.Cron = cronFromPreset(preset, cronCustom)
	cfg.Schedule.Timezone = strings.TrimSpace(tz)

	cfg.Resources.ImagesDangling = ruleFromMode(mDangling)
	cfg.Resources.ImagesUnused = ruleFromMode(mUnused)
	cfg.Resources.Containers = ruleFromMode(mContainers)
	cfg.Resources.Networks = ruleFromMode(mNetworks)
	cfg.Resources.BuildCache = ruleFromMode(mBuild)
	cfg.Resources.Volumes = ruleFromMode(mVolumes)

	cfg.Safety.MinAgeHours = atoiOr(minAge, 0)
	cfg.Safety.DryRun = dryRun
	cfg.Safety.ProtectLabels = splitList(protectLabels)
	cfg.Safety.ProtectVolumeNames = splitList(protectVols)

	cfg.Notify.Telegram.Enabled = telegramOn
	cfg.Notify.Telegram.BotToken = strings.TrimSpace(botToken)
	cfg.Notify.Telegram.ChatID = strings.TrimSpace(chatID)
	cfg.Notify.Telegram.ApprovalTimeoutMinutes = atoiOr(approvalTimeout, 120)
	cfg.Notify.Telegram.OnTimeout = onTimeout
	cfg.Notify.URLs = splitLines(shoutrrrURLs)

	if err := config.Save(path, cfg); err != nil {
		return fmt.Errorf("Speichern fehlgeschlagen: %w", err)
	}
	fmt.Printf("✓ Gespeichert nach %s\n", path)

	// --- validate & connection test ---
	for _, p := range cfg.Validate() {
		fmt.Println("⚠  " + p)
	}
	fmt.Print("Teste Docker-Verbindung … ")
	if err := testConnection(cfg); err != nil {
		fmt.Printf("FEHLER: %v\n", err)
	} else {
		fmt.Println("OK")
	}

	fmt.Println("\nNächste Schritte:")
	fmt.Println("  dredge scan            # anzeigen, was bereinigt würde")
	fmt.Println("  dredge run --dry-run   # kompletter Probelauf")

	if !systemd.Available() {
		// Container-Betrieb: der interne Scheduler übernimmt den Zeitplan.
		fmt.Println("  dredge daemon          # interner Scheduler (Container-Betrieb)")
		return nil
	}
	if os.Geteuid() != 0 {
		fmt.Println("  sudo dredge install    # als systemd-Timer einplanen")
		return nil
	}
	if !firstRun {
		fmt.Println("  sudo dredge install    # Timer mit dem neuen Zeitplan aktualisieren")
		return nil
	}

	// Wir laufen als root auf einem systemd-Host – dann den Timer gleich
	// mit anbieten, statt den Nutzer den Befehl merken zu lassen.
	scheduleNow := true
	if err := huh.NewConfirm().
		Title("Jetzt als systemd-Timer einplanen?").
		Description("Schreibt dredge.service + dredge.timer und aktiviert den Timer.").
		Value(&scheduleNow).Run(); err != nil {
		return nil
	}
	if !scheduleNow {
		fmt.Println("\n  Später jederzeit: sudo dredge install")
		return nil
	}
	fmt.Println()
	return installCmd.RunE(installCmd, nil)
}

func testConnection(cfg *config.Config) error {
	cli, err := docker.New(cfg.Docker.Host, cfg.Docker.APIVersion, 15*time.Second)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return cli.Ping(ctx)
}

// ---- helpers ----

func modeOr(r config.ResourceRule) config.Mode {
	if !r.Enabled {
		return config.ModeOff
	}
	if r.Mode == "" {
		return config.ModeAsk
	}
	return r.Mode
}

func ruleFromMode(m string) config.ResourceRule {
	if m == "off" || m == "" {
		return config.ResourceRule{Enabled: false, Mode: config.ModeAsk}
	}
	return config.ResourceRule{Enabled: true, Mode: config.Mode(m)}
}

func presetFromCron(cron string) (preset, custom string) {
	switch strings.Join(strings.Fields(cron), " ") {
	case "0 9 * * 1":
		return "weekly", cron
	case "0 3 * * *":
		return "daily", cron
	case "0 */6 * * *":
		return "6h", cron
	default:
		if cron == "" {
			return "weekly", "0 9 * * 1"
		}
		return "custom", cron
	}
}

func cronFromPreset(preset, custom string) string {
	switch preset {
	case "weekly":
		return "0 9 * * 1"
	case "daily":
		return "0 3 * * *"
	case "6h":
		return "0 */6 * * *"
	default:
		if strings.TrimSpace(custom) == "" {
			return "0 9 * * 1"
		}
		return strings.TrimSpace(custom)
	}
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}

func splitLines(s string) []string {
	var out []string
	for _, p := range strings.FieldsFunc(s, func(r rune) bool { return r == '\n' || r == '\r' }) {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}

func validateInt(s string) error {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	if _, err := strconv.Atoi(s); err != nil {
		return fmt.Errorf("bitte eine Zahl eingeben")
	}
	return nil
}

func validateCronMaybe(s string) error {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	if len(strings.Fields(s)) != 5 {
		return fmt.Errorf("cron braucht 5 Felder: Minute Stunde Tag Monat Wochentag")
	}
	return nil
}

func atoiOr(s string, def int) int {
	if n, err := strconv.Atoi(strings.TrimSpace(s)); err == nil {
		return n
	}
	return def
}

func orInt(v, def int) int {
	if v == 0 {
		return def
	}
	return v
}

func orStr(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
