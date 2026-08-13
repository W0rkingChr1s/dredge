// Package cmd wires up the dredge command-line interface.
package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/W0rkingChr1s/dredge/internal/config"
)

// Version is set at build time via -ldflags.
var Version = "dev"

var cfgPath string

var rootCmd = &cobra.Command{
	Use:   "dredge",
	Short: "Geführtes, sicheres Aufräumen für Docker – mit Telegram-Freigabe",
	Long: `dredge räumt ungenutzte Docker-Ressourcen auf (Images, Container,
Netzwerke, Build-Cache, Volumes) – mit Safety-Rails, planbar per Schedule und
mit interaktiver Freigabe über Telegram oder anderen Kanälen.

Erststart:  dredge setup
Testlauf:   dredge scan
Bereinigen: dredge run
Einplanen:  sudo dredge install   (systemd-Timer)`,
	SilenceUsage:  true,
	SilenceErrors: true,
}

// Execute runs the CLI.
func Execute(version string) {
	Version = version
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "Fehler:", err)
		os.Exit(1)
	}
}

func init() {
	rootCmd.PersistentFlags().StringVar(&cfgPath, "config", "", "Pfad zur config.yaml (Standard: automatisch)")
}

func configFile() string {
	if cfgPath != "" {
		return cfgPath
	}
	return config.DefaultPath()
}

// loadConfig loads the config or exits with a helpful hint.
func loadConfig() (*config.Config, string, error) {
	path := configFile()
	cfg, err := config.Load(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, path, fmt.Errorf("keine Konfiguration unter %s – bitte zuerst `dredge setup` ausführen", path)
		}
		return nil, path, err
	}
	return cfg, path, nil
}
