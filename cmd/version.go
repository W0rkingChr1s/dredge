package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Version anzeigen",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("dredge %s\n", Version)
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}
