// Copyright 2026 Bjørn Erik Pedersen
// SPDX-License-Identifier: Apache-2.0

package lib

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type Syncer struct {
	Cfg Config
	out io.Writer
}

func Sync(cfg Config) error {
	out := io.Writer(os.Stderr)
	if cfg.Quiet {
		out = io.Discard
	}
	s := &Syncer{Cfg: cfg, out: out}
	result, err := s.run()
	if err != nil {
		return err
	}
	s.printResult(result)
	return nil
}

func (s *Syncer) log(format string, a ...any) {
	fmt.Fprintf(s.out, format, a...)
}

func (s *Syncer) printResult(r Result) {
	if len(r.Updated) > 0 {
		s.log("Updated: %d repos\n", len(r.Updated))
		for _, repo := range r.Updated {
			if repo.Detail != "" {
				s.log("  - %s (%s)\n", repo.Path, repo.Detail)
			} else {
				s.log("  - %s\n", repo.Path)
			}
		}
	}

	if len(r.Cloned) > 0 {
		s.log("Cloned: %d repos\n", len(r.Cloned))
		for _, repo := range r.Cloned {
			s.log("  - %s\n", repo.Path)
		}
	}

	if len(r.Removed) > 0 {
		s.log("Removed: %d repos\n", len(r.Removed))
		for _, path := range r.Removed {
			s.log("  - %s\n", path)
		}
	}

	var uncommitted, nonDefault []SkippedRepo
	for _, skip := range r.Skipped {
		if skip.Reason == "uncommitted changes" {
			uncommitted = append(uncommitted, skip)
		} else {
			nonDefault = append(nonDefault, skip)
		}
	}

	if len(uncommitted) > 0 {
		s.log("Skipped (uncommitted changes): %d repos\n", len(uncommitted))
		for _, skip := range uncommitted {
			s.log("  - %s (%s)\n", skip.Path, skip.Detail)
		}
	}

	if len(nonDefault) > 0 {
		s.log("Skipped (non-default branch): %d repos\n", len(nonDefault))
		for _, skip := range nonDefault {
			s.log("  - %s (%s)\n", skip.Path, skip.Detail)
		}
	}

	if len(r.Warnings) > 0 {
		s.log("Warnings:\n")
		for _, w := range r.Warnings {
			s.log("  - %s\n", w)
		}
	}
}

func (s *Syncer) run() (Result, error) {
	var result Result

	expected, err := s.collectExpectedRepos()
	if err != nil {
		return result, err
	}

	existing := make(map[string]bool)

	for localPath, repoPath := range expected {
		fullPath := filepath.Join(s.Cfg.Root, localPath)
		existing[localPath] = true

		if _, err := os.Stat(fullPath); os.IsNotExist(err) {
			url := repoPathToURL(repoPath)
			if err := clone(url, fullPath, s.out); err != nil {
				result.Warnings = append(result.Warnings, "clone failed: "+localPath+": "+err.Error())
				continue
			}
			result.Cloned = append(result.Cloned, RepoResult{Path: localPath})
			continue
		}

		repo := Repo{Path: fullPath}

		if !repo.IsGitRepo() {
			result.Warnings = append(result.Warnings, "not a git repo: "+localPath)
			continue
		}

		defaultBranch, err := repo.DefaultBranch()
		if err != nil {
			result.Warnings = append(result.Warnings, "could not get default branch: "+localPath+": "+err.Error())
			continue
		}

		currentBranch, err := repo.CurrentBranch()
		if err != nil {
			result.Warnings = append(result.Warnings, "could not get current branch: "+localPath+": "+err.Error())
			continue
		}

		dirty, err := repo.HasUncommittedChanges()
		if err != nil {
			result.Warnings = append(result.Warnings, "could not check for uncommitted changes: "+localPath+": "+err.Error())
			continue
		}

		if !s.Cfg.Force {
			if dirty {
				result.Skipped = append(result.Skipped, SkippedRepo{
					Path:   localPath,
					Reason: "uncommitted changes",
					Detail: repo.ChangesSummary(),
				})
				continue
			}
			if currentBranch != defaultBranch {
				result.Skipped = append(result.Skipped, SkippedRepo{
					Path:   localPath,
					Reason: "non-default branch",
					Detail: "on " + currentBranch,
				})
				continue
			}
			changed, err := repo.Pull()
			if err != nil {
				result.Warnings = append(result.Warnings, "pull failed: "+localPath+": "+err.Error())
				continue
			}
			if changed {
				result.Updated = append(result.Updated, RepoResult{Path: localPath, Detail: "pulled"})
			}
		} else {
			var details []string
			stashed := false
			if dirty {
				if err := repo.Stash(); err != nil {
					result.Warnings = append(result.Warnings, "stash failed: "+localPath+": "+err.Error())
					continue
				}
				stashed = true
				details = append(details, "stashed")
			}
			if currentBranch != defaultBranch {
				if err := repo.SwitchBranch(defaultBranch); err != nil {
					result.Warnings = append(result.Warnings, "switch branch failed: "+localPath+": "+err.Error())
					continue
				}
				details = append(details, "switched to "+defaultBranch)
			}
			changed, err := repo.Pull()
			if err != nil {
				result.Warnings = append(result.Warnings, "pull failed: "+localPath+": "+err.Error())
				continue
			}
			if changed {
				details = append(details, "pulled")
			}
			if stashed {
				if err := repo.Unstash(); err != nil {
					result.Warnings = append(result.Warnings, "unstash failed (conflicts?): "+localPath+": "+err.Error())
				} else {
					details = append(details, "unstashed")
				}
			}
			if len(details) > 0 {
				result.Updated = append(result.Updated, RepoResult{Path: localPath, Detail: strings.Join(details, ", ")})
			}
		}
	}

	allRepos, err := s.findAllGitRepos()
	if err != nil {
		return result, err
	}
	for _, repo := range allRepos {
		if !existing[repo] {
			fullPath := filepath.Join(s.Cfg.Root, repo)
			if err := os.RemoveAll(fullPath); err != nil {
				result.Warnings = append(result.Warnings, "remove failed: "+repo+": "+err.Error())
				continue
			}
			result.Removed = append(result.Removed, repo)
		}
	}

	return result, nil
}

func (s *Syncer) collectExpectedRepos() (map[string]string, error) {
	expected := make(map[string]string)

	err := filepath.WalkDir(s.Cfg.Root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && d.Name() == ".git" {
			return filepath.SkipDir
		}
		if d.Name() != "gitjoin.txt" {
			return nil
		}

		dir := filepath.Dir(path)
		relDir, err := filepath.Rel(s.Cfg.Root, dir)
		if err != nil {
			return err
		}

		repos, err := parseGitjoinFile(path)
		if err != nil {
			return err
		}

		for _, repo := range repos {
			repoName := filepath.Base(repo)
			var localPath string
			if relDir == "." {
				localPath = repoName
			} else {
				localPath = filepath.Join(relDir, repoName)
			}

			if s.Cfg.Paths != "" {
				matched, err := filepath.Match(s.Cfg.Paths, localPath)
				if err != nil {
					return err
				}
				if !matched {
					continue
				}
			}

			expected[localPath] = repo
		}
		return nil
	})

	return expected, err
}

func (s *Syncer) findAllGitRepos() ([]string, error) {
	var repos []string
	err := filepath.WalkDir(s.Cfg.Root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && d.Name() == ".git" {
			rel, err := filepath.Rel(s.Cfg.Root, filepath.Dir(path))
			if err != nil {
				return err
			}
			if rel != "." {
				repos = append(repos, rel)
			}
			return filepath.SkipDir
		}
		return nil
	})
	return repos, err
}

func parseGitjoinFile(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var repos []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		repos = append(repos, line)
	}
	return repos, scanner.Err()
}

func repoPathToURL(repoPath string) string {
	parts := strings.SplitN(repoPath, "/", 2)
	if len(parts) != 2 {
		return ""
	}
	return fmt.Sprintf("git@%s:%s.git", parts[0], parts[1])
}
