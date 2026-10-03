// Command hubtui is a terminal UI for browsing Docker Hub.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime/debug"
)

// version is set at build time via -ldflags "-X main.version=...".
var version = "" //nolint:gochecknoglobals // the linker can only inject into package-level vars

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("hubtui", flag.ContinueOnError)
	fs.SetOutput(stderr)
	showVersion := fs.Bool("version", false, "print version and exit")
	fs.Usage = func() {
		_, _ = fmt.Fprintln(fs.Output(), "Usage: hubtui [flags]")
		fs.PrintDefaults()
	}

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}

	if *showVersion {
		_, _ = fmt.Fprintf(stdout, "hubtui %s\n", resolveVersion(version))
		return 0
	}

	fs.Usage()
	return 2
}

// resolveVersion falls back to module build info so `go install ...@vX`
// reports a real version even without the Makefile's ldflags.
func resolveVersion(injected string) string {
	if injected != "" {
		return injected
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "dev"
}
