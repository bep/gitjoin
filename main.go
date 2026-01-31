// Copyright 2026 Bjørn Erik Pedersen
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/bep/gitjoin/internal/lib"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	var cfg lib.Config

	flag.BoolVar(&cfg.Force, "force", false, "force sync: stash changes, switch to default branch")
	flag.BoolVar(&cfg.Quiet, "quiet", false, "suppress all output")
	flag.StringVar(&cfg.Paths, "paths", "", "glob filter for repo paths")
	flag.Parse()

	wd, err := os.Getwd()
	if err != nil {
		return err
	}
	cfg.Root = wd

	return lib.Sync(cfg)
}
