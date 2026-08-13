package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/robfig/cron/v3"
	"github.com/spf13/cobra"

	"github.com/W0rkingChr1s/dredge/internal/config"
	"github.com/W0rkingChr1s/dredge/internal/runner"
)

var daemonRunOnStart bool

var daemonCmd = &cobra.Command{
	Use:   "daemon",
	Short: "Im Vordergrund laufen und nach Schedule bereinigen (für Container)",
	Long: `Startet einen internen Scheduler, der den Bereinigungslauf gemäß
schedule.cron ausführt. Gedacht für den Container-Betrieb (kein systemd).
Läuft im Vordergrund und reagiert auf SIGINT/SIGTERM.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, path, err := loadConfig()
		if err != nil {
			return err
		}
		loc := time.Local
		if cfg.Schedule.Timezone != "" {
			if l, err := time.LoadLocation(cfg.Schedule.Timezone); err == nil {
				loc = l
			} else {
				fmt.Fprintf(os.Stderr, "⚠  Zeitzone %q unbekannt, nutze lokale Zeit\n", cfg.Schedule.Timezone)
			}
		}

		ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
		defer stop()

		doRun := func() {
			// Reload config each run so edits via `settings` take effect.
			fresh, err := config.Load(path)
			if err != nil {
				fresh = cfg
			}
			fmt.Printf("[%s] Starte Lauf …\n", time.Now().Format(time.RFC3339))
			_, _, err = runner.Run(ctx, fresh, runner.Options{
				Log: func(m string) { fmt.Println("  • " + m) },
			})
			if err != nil {
				fmt.Fprintln(os.Stderr, "  Fehler:", err)
			}
		}

		c := cron.New(cron.WithLocation(loc))
		if _, err := c.AddFunc(cfg.Schedule.Cron, doRun); err != nil {
			return fmt.Errorf("ungültiger cron %q: %w", cfg.Schedule.Cron, err)
		}
		c.Start()
		defer c.Stop()

		if entries := c.Entries(); len(entries) > 0 {
			fmt.Printf("dredge daemon läuft. Nächster Lauf: %s (cron: %s, %s)\n",
				entries[0].Next.Format(time.RFC3339), cfg.Schedule.Cron, loc.String())
		}
		if daemonRunOnStart {
			doRun()
		}

		<-ctx.Done()
		fmt.Println("\nBeende …")
		return nil
	},
}

func init() {
	daemonCmd.Flags().BoolVar(&daemonRunOnStart, "run-on-start", false, "einmal sofort beim Start ausführen")
	rootCmd.AddCommand(daemonCmd)
}
