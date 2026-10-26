// Package app wires honk's commands together.
package app

import (
	"os"
	"runtime/debug"

	"github.com/urfave/cli/v3"
)

// version can be set at build time with -ldflags "-X github.com/immannino/honk/internal/app.version=...".
// Otherwise it comes from the module version recorded by `go install ...@vX.Y.Z`.
var version = ""

func resolveVersion() string {
	if version != "" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "dev"
}

// New returns the root command.
func New() *cli.Command {
	return &cli.Command{
		Name:                  "honk",
		Usage:                 "keep an eye on every git repo in a directory tree",
		Version:               resolveVersion(),
		EnableShellCompletion: true,
		DefaultCommand:        "status",
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:  "no-color",
				Usage: "disable colored output (also honors NO_COLOR)",
			},
		},
		Commands: []*cli.Command{
			statusCommand(),
			syncCommand(),
		},
	}
}

func useColor(cmd *cli.Command) bool {
	if cmd.Bool("no-color") || os.Getenv("NO_COLOR") != "" {
		return false
	}
	fi, err := os.Stdout.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}
