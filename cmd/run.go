package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/W0rkingChr1s/dredge/internal/runner"
)

var (
	runDryRun bool
	runYes    bool
)

var runCmd = &cobra.Command{
	Use:   "run",
	Short: "Bereinigungslauf ausführen (Scan → Freigabe → Bereinigen)",
	Long: `Führt einen vollständigen Lauf aus: scannt den Docker-Host, sendet bei
'ask'-Typen eine Telegram-Freigabe mit Buttons, bereinigt 'auto'-Typen direkt
und schickt am Ende eine Bestätigung.

Dieser Befehl wird typischerweise vom systemd-Timer bzw. dem Container-Scheduler
aufgerufen.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, _, err := loadConfig()
		if err != nil {
			return err
		}
		if problems := cfg.Validate(); len(problems) > 0 {
			for _, p := range problems {
				fmt.Fprintln(os.Stderr, "⚠  "+p)
			}
		}

		ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
		defer stop()

		_, _, err = runner.Run(ctx, cfg, runner.Options{
			ForceDryRun: runDryRun,
			Yes:         runYes,
			Log:         func(m string) { fmt.Println("• " + m) },
		})
		return err
	},
}

func init() {
	runCmd.Flags().BoolVar(&runDryRun, "dry-run", false, "nichts löschen, nur simulieren")
	runCmd.Flags().BoolVarP(&runYes, "yes", "y", false, "alle 'ask'-Typen ohne Nachfrage freigeben (nicht-interaktiv)")
	rootCmd.AddCommand(runCmd)
}
