package main

import (
	"github.com/bencornia/ptools/internal/cli"
	"github.com/bencornia/ptools/internal/doctor"
	"github.com/spf13/cobra"
)

func doctorCommand() *cobra.Command {
	var fix bool
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Check that ptools, the P compiler and your editors are set up",
		Long: "doctor checks that ptools is set up to work: that plsp, prdb, pfmt and pdoc\n" +
			"are installed and on the PATH, that plsp and prdb can run the P compiler,\n" +
			"and that Helix and VS Code, if you have them, are set up for P.\n\n" +
			"With --fix, it fixes what it can: it installs the P compiler (and .NET 8,\n" +
			"into ~/.dotnet, if need be), sets up P in Helix's languages.toml and builds\n" +
			"the grammar for it, and installs the VS Code extension. It exits with\n" +
			"status 1 if anything is still wrong.",
		Args: cli.Args(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			if doctor.New(cmd.OutOrStdout(), fix).Run() {
				return nil
			}
			return cli.Exit(1)
		},
	}
	cmd.Flags().BoolVar(&fix, "fix", false, "fix what it can")
	return cmd
}
