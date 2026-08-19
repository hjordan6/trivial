// Command trivial administers the daily trivia content library and puzzles.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/hjordan6/trivial/internal/cli"
)

func main() {
	if err := cli.Run(context.Background(), os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
