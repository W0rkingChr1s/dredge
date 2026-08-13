package cmd

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/W0rkingChr1s/dredge/internal/docker"
	"github.com/W0rkingChr1s/dredge/internal/janitor"
	"github.com/W0rkingChr1s/dredge/internal/report"
)

var scanCmd = &cobra.Command{
	Use:   "scan",
	Short: "Nur scannen und anzeigen, was bereinigt würde (ändert nichts)",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, _, err := loadConfig()
		if err != nil {
			return err
		}
		cli, err := docker.New(cfg.Docker.Host, cfg.Docker.APIVersion,
			time.Duration(cfg.Docker.TimeoutSeconds)*time.Second)
		if err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		if err := cli.Ping(ctx); err != nil {
			return fmt.Errorf("Docker-Host nicht erreichbar (%s): %w", cfg.Docker.Host, err)
		}
		plan, err := janitor.Scan(ctx, cli, cfg)
		if err != nil {
			return err
		}
		fmt.Print(report.OverviewPlain(cfg.Docker.Host, plan))
		return nil
	},
}

func init() {
	rootCmd.AddCommand(scanCmd)
}
