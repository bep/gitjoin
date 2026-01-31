// Copyright 2026 Bjørn Erik Pedersen
// SPDX-License-Identifier: Apache-2.0

package lib

type Config struct {
	Root  string
	Force bool
	Quiet bool
	Paths string // glob filter (optional)
}

type Result struct {
	Updated  []RepoResult
	Cloned   []RepoResult
	Removed  []string
	Skipped  []SkippedRepo
	Warnings []string
}

type RepoResult struct {
	Path   string
	Detail string
}

type SkippedRepo struct {
	Path   string
	Reason string
	Detail string
}
