// Command glossa is Glossa's CLI: init, login, push, pull, extract,
// generate, check, status, diff, locales, messages, import, export,
// jobs, release and the knowledge and AI commands, for developers and
// CI. See README.md; the commands live in
// internal/cli.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"golang.org/x/term"

	"go.klarlabs.de/glossa/platform/internal/cli"
)

// version is set at build time (-ldflags "-X main.version=…").
var version = "dev"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := cli.Main(ctx, os.Args[1:], cli.Env{
		Stdin:       os.Stdin,
		Stdout:      os.Stdout,
		Stderr:      os.Stderr,
		Getenv:      os.Getenv,
		Dir:         workingDir(),
		Interactive: term.IsTerminal(int(os.Stdin.Fd())),
		ColorOutput: term.IsTerminal(int(os.Stdout.Fd())) && os.Getenv("TERM") != "dumb",
		ReadSecret:  readSecret,
		Version:     version,
	})
	stop()
	os.Exit(code)
}

func workingDir() string {
	dir, err := os.Getwd()
	if err != nil {
		return "."
	}
	return dir
}

// readSecret prompts on stderr and reads without echo.
func readSecret(prompt string) (string, error) {
	fmt.Fprint(os.Stderr, prompt)
	b, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	return string(b), err
}
