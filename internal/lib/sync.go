// Copyright 2026 Bjørn Erik Pedersen
// SPDX-License-Identifier: Apache-2.0

package lib

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"

	"github.com/bep/helpers/parahelpers"
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
}

func (s *Syncer) run() (Result, error) {
	var result Result
	var mu sync.Mutex
	var existing sync.Map

	expected, err := s.collectExpectedRepos()
	if err != nil {
		return result, err
	}

	numWorkers := max(4, runtime.NumCPU())
	workers := parahelpers.New(numWorkers)
	r, ctx := workers.Start(context.Background())

	for localPath, repoPath := range expected {
		r.Run(func() error {
			return s.processRepo(ctx, localPath, repoPath, &existing, &result, &mu)
		})
	}

	if err := r.Wait(); err != nil {
		return result, err
	}

	allRepos, err := s.findAllGitRepos()
	if err != nil {
		return result, err
	}
	for _, repo := range allRepos {
		if _, found := existing.Load(repo); !found {
			fullPath := filepath.Join(s.Cfg.Root, repo)
			if err := os.RemoveAll(fullPath); err != nil {
				return result, fmt.Errorf("remove %s: %w", repo, err)
			}
			result.Removed = append(result.Removed, repo)
		}
	}

	if err := s.updateGitignore(expected); err != nil {
		return result, fmt.Errorf("update .gitignore: %w", err)
	}

	return result, nil
}

func (s *Syncer) processRepo(ctx context.Context, localPath, repoPath string, existing *sync.Map, result *Result, mu *sync.Mutex) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	fullPath := filepath.Join(s.Cfg.Root, localPath)
	existing.Store(localPath, true)

	if _, err := os.Stat(fullPath); os.IsNotExist(err) {
		url := repoPathToURL(repoPath)
		if err := clone(url, fullPath, s.out); err != nil {
			return fmt.Errorf("clone %s: %w", localPath, err)
		}
		mu.Lock()
		result.Cloned = append(result.Cloned, RepoResult{Path: localPath})
		mu.Unlock()
		return nil
	}

	repo := Repo{Path: fullPath}

	if !repo.IsGitRepo() {
		return fmt.Errorf("%s: not a git repo", localPath)
	}

	defaultBranch, err := repo.DefaultBranch()
	if err != nil {
		return fmt.Errorf("%s: get default branch: %w", localPath, err)
	}

	currentBranch, err := repo.CurrentBranch()
	if err != nil {
		return fmt.Errorf("%s: get current branch: %w", localPath, err)
	}

	dirty, err := repo.HasUncommittedChanges()
	if err != nil {
		return fmt.Errorf("%s: check uncommitted changes: %w", localPath, err)
	}

	if !s.Cfg.Force {
		if dirty {
			mu.Lock()
			result.Skipped = append(result.Skipped, SkippedRepo{
				Path:   localPath,
				Reason: "uncommitted changes",
				Detail: repo.ChangesSummary(),
			})
			mu.Unlock()
			return nil
		}
		if currentBranch != defaultBranch {
			mu.Lock()
			result.Skipped = append(result.Skipped, SkippedRepo{
				Path:   localPath,
				Reason: "non-default branch",
				Detail: "on " + currentBranch,
			})
			mu.Unlock()
			return nil
		}
		changed, err := repo.Pull()
		if err != nil {
			return fmt.Errorf("%s: pull: %w", localPath, err)
		}
		if changed {
			mu.Lock()
			result.Updated = append(result.Updated, RepoResult{Path: localPath, Detail: "pulled"})
			mu.Unlock()
		}
	} else {
		var details []string
		stashed := false
		if dirty {
			if err := repo.Stash(); err != nil {
				return fmt.Errorf("%s: stash: %w", localPath, err)
			}
			stashed = true
			details = append(details, "stashed")
		}
		if currentBranch != defaultBranch {
			if err := repo.SwitchBranch(defaultBranch); err != nil {
				return fmt.Errorf("%s: switch branch: %w", localPath, err)
			}
			details = append(details, "switched to "+defaultBranch)
		}
		changed, err := repo.Pull()
		if err != nil {
			return fmt.Errorf("%s: pull: %w", localPath, err)
		}
		if changed {
			details = append(details, "pulled")
		}
		if stashed {
			if err := repo.Unstash(); err != nil {
				return fmt.Errorf("%s: unstash: %w", localPath, err)
			}
			details = append(details, "unstashed")
		}
		if len(details) > 0 {
			mu.Lock()
			result.Updated = append(result.Updated, RepoResult{Path: localPath, Detail: strings.Join(details, ", ")})
			mu.Unlock()
		}
	}
	return nil
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
	if os.Getenv("GITHUB_ACTIONS") != "" {
		return fmt.Sprintf("https://%s/%s.git", parts[0], parts[1])
	}
	return fmt.Sprintf("git@%s:%s.git", parts[0], parts[1])
}

const (
	gitignoreStart = "# Managed by gitjoin - do not edit this section"
	gitignoreEnd   = "# End gitjoin managed section"
)

func (s *Syncer) updateGitignore(repos map[string]string) error {
	gitignorePath := filepath.Join(s.Cfg.Root, ".gitignore")

	var paths []string
	for localPath := range repos {
		paths = append(paths, filepath.ToSlash(localPath)+"/")
	}
	sort.Strings(paths)

	var managed strings.Builder
	managed.WriteString(gitignoreStart + "\n")
	for _, p := range paths {
		managed.WriteString(p + "\n")
	}
	managed.WriteString(gitignoreEnd + "\n")

	existing, err := os.ReadFile(gitignorePath)
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	var newContent string
	if len(existing) == 0 {
		newContent = managed.String()
	} else {
		content := string(existing)
		startIdx := strings.Index(content, gitignoreStart)
		endIdx := strings.Index(content, gitignoreEnd)

		if startIdx >= 0 && endIdx > startIdx {
			endIdx += len(gitignoreEnd)
			if endIdx < len(content) && content[endIdx] == '\n' {
				endIdx++
			}
			newContent = content[:startIdx] + managed.String() + content[endIdx:]
		} else {
			if !strings.HasSuffix(content, "\n") {
				content += "\n"
			}
			newContent = content + "\n" + managed.String()
		}
	}

	return os.WriteFile(gitignorePath, []byte(newContent), 0o644)
}
