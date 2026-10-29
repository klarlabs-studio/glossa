// Command fixturegen writes the M3 exit test's fixture application
// (package fixture) into a directory:
//
//	go run ./internal/systemtest/m3/fixturegen -out internal/systemtest/m3/testdata
//
// The output is a pure function of -seed; the committed files were
// written with fixture.DefaultSeed. Rebuild the app afterwards with
// `pnpm --filter @klarlabs-studio/glossa-unplugin build:m3`.
package main

import (
	"flag"
	"fmt"
	"os"

	"go.klarlabs.de/glossa/platform/internal/systemtest/m3/fixture"
)

func main() {
	seed := flag.Int64("seed", fixture.DefaultSeed, "generator seed")
	out := flag.String("out", "testdata", "output directory")
	flag.Parse()
	f, err := fixture.Generate(*seed)
	if err != nil {
		fmt.Fprintln(os.Stderr, "fixturegen:", err)
		os.Exit(1)
	}
	if err := fixture.Write(f, *out); err != nil {
		fmt.Fprintln(os.Stderr, "fixturegen:", err)
		os.Exit(1)
	}
	fmt.Printf("%d messages over %d routes, %d server-side usages; %d new keys on %s (invalid at %s:%d)\n",
		len(f.Messages), len(f.Pages), f.GoUsages(), len(f.NewKeys), fixture.PRBranch, f.InvalidFile, f.InvalidLine)
}
