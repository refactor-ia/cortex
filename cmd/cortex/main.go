package main

import (
	"context"
	"os"

	"github.com/refactor-ia/cortex/internal/cli"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr *os.File) int {
	return cli.Run(context.Background(), args, stdout, stderr, nil)
}
