package mgr

import (
	"fmt"
	"github.com/spf13/cobra"
	"os"
)

const Version = "v0.0.2"

var VersionCmd = &cobra.Command{
	Use:   "version",
	Short: "version subcommand show idefav proxy version info.",

	Run: func(cmd *cobra.Command, args []string) {
		fmt.Fprint(os.Stdout, Version)
	},
}
