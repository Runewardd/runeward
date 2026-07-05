// Command runeward is the CLI entrypoint: it resolves declarative profiles and
// provisions governed agent sandboxes.
package main

import (
	"fmt"
	"os"

	"github.com/Runewardd/runeward/internal/cli"
)

func main() {
	if err := cli.Execute(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
