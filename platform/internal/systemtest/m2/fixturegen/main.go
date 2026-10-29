// Command fixturegen writes the M2 exit test's fixture (package
// fixture) into a directory:
//
//	go run ./internal/systemtest/m2/fixturegen -out internal/systemtest/m2/testdata
//
// The output is a pure function of -seed; the committed files were
// written with fixture.DefaultSeed.
package main

import (
	"flag"
	"fmt"
	"os"

	"go.klarlabs.de/glossa/platform/internal/systemtest/m2/fixture"
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
	for _, l := range f.FillLocales {
		e := f.Expect[l]
		fmt.Printf("%s: %d messages, %d existing, %d missing (%d sensitive), %d TM, %d AI (%d TM-blocked), slips %v\n",
			l, e.Messages, e.Existing, e.Missing, e.Sensitive, e.TMReused, e.AIDrafted, e.TMBlocked, e.Slips)
	}
}
