// Command glossa-edge is Glossa's delivery plane (RFC 0002 §3,
// runtimes/SPEC.md §2): a stateless server that answers runtimes'
// manifest and artifact requests from object storage. It has no
// database connection and no dependency on glossa-server, so published
// translations keep loading while the control plane is down. This file
// is the composition root and nothing else.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"go.klarlabs.de/glossa/platform/internal/edge"
	"go.klarlabs.de/glossa/platform/internal/kernel/buildinfo"
	"go.klarlabs.de/glossa/platform/internal/kernel/config"
	"go.klarlabs.de/glossa/platform/internal/kernel/observability"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.LookupEnv, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "glossa-edge:", err)
		os.Exit(1)
	}
}

// run is main without process globals, so tests can drive it.
func run(ctx context.Context, args []string, lookup config.LookupFunc, stdout io.Writer) error {
	fs := flag.NewFlagSet("glossa-edge", flag.ContinueOnError)
	showVersion := fs.Bool("version", false, "print the version and exit")
	if err := fs.Parse(args); errors.Is(err, flag.ErrHelp) {
		return nil
	} else if err != nil {
		return err
	}
	if *showVersion {
		_, err := fmt.Fprintln(stdout, version())
		return err
	}
	cfg, err := config.LoadEdge(lookup)
	if err != nil {
		return err
	}
	logger := observability.NewLogger(stdout, cfg.LogLevel)
	logger.InfoContext(ctx, "glossa-edge starting", slog.String("version", version()), slog.String("revision", buildinfo.Revision()), slog.String("config", cfg.String()))
	return edge.Run(ctx, cfg, logger, version(), nil)
}

// version is the build's release version (see kernel/buildinfo).
func version() string { return buildinfo.Version() }
