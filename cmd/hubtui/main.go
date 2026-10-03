// Command hubtui is a terminal UI for browsing Docker Hub.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"runtime/debug"
	"strings"

	"github.com/davidhfrankelcodes/hubtui/internal/browser"
	"github.com/davidhfrankelcodes/hubtui/internal/clip"
	"github.com/davidhfrankelcodes/hubtui/internal/config"
	"github.com/davidhfrankelcodes/hubtui/internal/hub"
	"github.com/davidhfrankelcodes/hubtui/internal/tui"
)

// version is set at build time via -ldflags "-X main.version=...".
var version = "" //nolint:gochecknoglobals // the linker can only inject into package-level vars

const usage = `Usage:
  hubtui                          search Docker Hub interactively
  hubtui <image>                  browse an image's tags interactively
  hubtui --version

Set DOCKERHUB_USERNAME and DOCKERHUB_TOKEN (a personal access token) to
make authenticated requests; anonymous access is the default.
  hubtui search <query> --json
  hubtui tags <image> --json [--arch <arch>] [--limit <n>]
`

const (
	exitOK    = 0
	exitError = 1
	exitUsage = 2
)

// app holds what commands need, so tests can swap the registry, streams and
// terminal.
type app struct {
	stdout, stderr io.Writer
	registry       hub.Registry
	// stdoutIsTTY gates the TUI: piped output gets usage, not escape codes.
	stdoutIsTTY bool
	// openTUI starts on the Tags screen for repo, or on Search when nil.
	openTUI func(context.Context, *hub.Repo) error
}

func main() {
	os.Exit(mainCode())
}

// mainCode exists so deferred cleanup runs before os.Exit.
func mainCode() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	cfg, err := config.FromEnv(os.Getenv)
	if err != nil {
		fmt.Fprintf(os.Stderr, "hubtui: %v\n", err)
		return exitUsage
	}
	client, err := hub.NewClient(hub.Options{
		UserAgent: "hubtui/" + resolveVersion(version),
		Username:  cfg.Username,
		Token:     cfg.Token,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "hubtui: %v\n", err)
		return exitError
	}
	a := &app{
		stdout:      os.Stdout,
		stderr:      os.Stderr,
		registry:    client,
		stdoutIsTTY: isTerminal(os.Stdout),
		openTUI: func(ctx context.Context, repo *hub.Repo) error {
			return tui.Run(ctx, tui.Deps{
				Registry:  hub.NewCache(client, hub.DefaultCacheTTL),
				Clipboard: clip.NewNative(),
				Browser:   browser.New(),
				User:      cfg.Username,
			}, repo)
		},
	}
	return a.run(ctx, os.Args[1:])
}

func (a *app) run(ctx context.Context, args []string) int {
	if len(args) > 0 {
		switch args[0] {
		case "search":
			return a.runSearch(ctx, args[1:])
		case "tags":
			return a.runTags(ctx, args[1:])
		}
	}

	fs := a.flagSet("hubtui", usage)
	showVersion := fs.Bool("version", false, "print version and exit")
	var pos []string
	if code, ok := a.parse(fs, args, &pos); !ok {
		return code
	}
	if *showVersion {
		_, _ = fmt.Fprintf(a.stdout, "hubtui %s\n", resolveVersion(version))
		return exitOK
	}
	if len(pos) > 1 || !a.stdoutIsTTY {
		fs.Usage()
		return exitUsage
	}

	var repo *hub.Repo
	if len(pos) == 1 {
		r, err := hub.ParseRepo(pos[0])
		if err != nil {
			return a.usageError(fs, err.Error())
		}
		repo = &r
	}
	if err := a.openTUI(ctx, repo); err != nil {
		return a.fail(err)
	}
	return exitOK
}

func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

func (a *app) runSearch(ctx context.Context, args []string) int {
	fs := a.flagSet("search", "Usage: hubtui search <query> --json\n")
	asJSON := fs.Bool("json", false, "print results as JSON (required)")
	var pos []string
	if code, ok := a.parse(fs, args, &pos); !ok {
		return code
	}
	if len(pos) == 0 {
		return a.usageError(fs, "search: missing query")
	}
	if !*asJSON {
		return a.usageError(fs, "search: --json is required")
	}
	return a.searchJSON(ctx, strings.Join(pos, " "))
}

func (a *app) runTags(ctx context.Context, args []string) int {
	fs := a.flagSet("tags", "Usage: hubtui tags <image> --json [--arch <arch>] [--limit <n>]\n")
	asJSON := fs.Bool("json", false, "print tags as JSON (required)")
	arch := fs.String("arch", "", "only tags with this platform: arm64, linux/arm64, arm/v7, ...")
	limit := fs.Int("limit", 100, "maximum number of tags; 0 for all that Docker Hub will return")
	var pos []string
	if code, ok := a.parse(fs, args, &pos); !ok {
		return code
	}
	if len(pos) != 1 {
		return a.usageError(fs, "tags: expected exactly one image")
	}
	if !*asJSON {
		return a.usageError(fs, "tags: --json is required")
	}
	if *limit < 0 {
		return a.usageError(fs, "tags: --limit must not be negative")
	}
	repo, err := hub.ParseRepo(pos[0])
	if err != nil {
		return a.usageError(fs, "tags: "+err.Error())
	}
	return a.tagsJSON(ctx, repo, strings.ToLower(strings.TrimSpace(*arch)), *limit)
}

func (a *app) flagSet(name, synopsis string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(a.stderr)
	fs.Usage = func() {
		_, _ = fmt.Fprint(fs.Output(), synopsis)
		fs.PrintDefaults()
	}
	return fs
}

// parse parses flags anywhere among the arguments, since `hubtui tags nginx
// --json` is how people type it and the flag package stops at the first
// positional. Positionals go to pos; a nil pos means none are allowed.
func (a *app) parse(fs *flag.FlagSet, args []string, pos *[]string) (code int, ok bool) {
	var positional []string
	for {
		if err := fs.Parse(args); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return exitOK, false
			}
			return exitUsage, false
		}
		rest := fs.Args()
		if len(rest) == 0 {
			break
		}
		// After "--" everything is positional, even if it looks like a flag.
		if consumed := len(args) - len(rest); consumed > 0 && args[consumed-1] == "--" {
			positional = append(positional, rest...)
			break
		}
		positional = append(positional, rest[0])
		args = rest[1:]
	}

	if pos == nil && len(positional) > 0 {
		return a.usageError(fs, fmt.Sprintf("unexpected argument %q", positional[0])), false
	}
	if pos != nil {
		*pos = positional
	}
	return exitOK, true
}

func (a *app) usageError(fs *flag.FlagSet, msg string) int {
	_, _ = fmt.Fprintf(a.stderr, "hubtui: %s\n", msg)
	fs.Usage()
	return exitUsage
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
