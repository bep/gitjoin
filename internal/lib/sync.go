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

	sources, err := s.collectSources()
	if err != nil {
		return result, err
	}
	expected, err := s.expectedFromSources(sources)
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

	managed, err := s.findManagedRepos()
	if err != nil {
		return result, err
	}
	for _, repo := range managed {
		if _, found := existing.Load(repo); found {
			continue
		}
		fullPath := filepath.Join(s.Cfg.Root, repo)
		if !(Repo{Path: fullPath}).IsGitRepo() {
			continue
		}
		if err := os.RemoveAll(fullPath); err != nil {
			return result, fmt.Errorf("remove %s: %w", repo, err)
		}
		result.Removed = append(result.Removed, repo)
	}

	if err := s.updateGitignores(sources); err != nil {
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
			unmerged, err := repo.hasUnmergedPaths()
			if err != nil {
				return fmt.Errorf("%s: check unmerged paths: %w", localPath, err)
			}
			if unmerged {
				return fmt.Errorf("%s: has unmerged paths; resolve the merge conflict before syncing", localPath)
			}
			stashed, err = repo.Stash()
			if err != nil {
				return fmt.Errorf("%s: stash: %w", localPath, err)
			}
			if stashed {
				details = append(details, "stashed")
			}
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

type gitjoinSource struct {
	dir   string
	repos []string
}

func (s *Syncer) collectSources() ([]gitjoinSource, error) {
	var sources []gitjoinSource

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

		sources = append(sources, gitjoinSource{dir: relDir, repos: repos})
		return nil
	})

	return sources, err
}

func (s *Syncer) expectedFromSources(sources []gitjoinSource) (map[string]string, error) {
	expected := make(map[string]string)
	for _, src := range sources {
		for _, repo := range src.repos {
			repoName := filepath.Base(repo)
			var localPath string
			if src.dir == "." {
				localPath = repoName
			} else {
				localPath = filepath.Join(src.dir, repoName)
			}

			expected[localPath] = repo
		}
	}
	return expected, nil
}

// findManagedRepos returns the repos recorded in gitjoin-managed .gitignore
// sections. Only these are candidates for removal; git repos gitjoin never
// cloned (e.g. build artifacts) are left alone.
func (s *Syncer) findManagedRepos() ([]string, error) {
	var repos []string
	err := filepath.WalkDir(s.Cfg.Root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && d.Name() == ".git" {
			return filepath.SkipDir
		}
		if d.IsDir() || d.Name() != ".gitignore" {
			return nil
		}
		entries, err := readManagedEntries(path)
		if err != nil {
			return err
		}
		if len(entries) == 0 {
			return nil
		}
		relDir, err := filepath.Rel(s.Cfg.Root, filepath.Dir(path))
		if err != nil {
			return err
		}
		for _, e := range entries {
			if relDir == "." {
				repos = append(repos, e)
			} else {
				repos = append(repos, filepath.Join(relDir, e))
			}
		}
		return nil
	})
	return repos, err
}

func readManagedEntries(gitignorePath string) ([]string, error) {
	b, err := os.ReadFile(gitignorePath)
	if err != nil {
		return nil, err
	}
	content := string(b)
	startIdx := strings.Index(content, gitignoreStart)
	endIdx := strings.Index(content, gitignoreEnd)
	if startIdx < 0 || endIdx <= startIdx {
		return nil, nil
	}
	var entries []string
	for line := range strings.SplitSeq(content[startIdx+len(gitignoreStart):endIdx], "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		entries = append(entries, strings.TrimSuffix(line, "/"))
	}
	return entries, nil
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

func (s *Syncer) updateGitignores(sources []gitjoinSource) error {
	managedDirs := make(map[string]bool)
	for _, src := range sources {
		managedDirs[src.dir] = true
		if err := s.writeManagedSection(src); err != nil {
			return err
		}
	}
	return s.pruneStaleManaged(managedDirs)
}

func (s *Syncer) writeManagedSection(src gitjoinSource) error {
	gitignorePath := filepath.Join(s.Cfg.Root, src.dir, ".gitignore")

	var entries []string
	for _, repo := range src.repos {
		entries = append(entries, filepath.Base(repo)+"/")
	}
	sort.Strings(entries)

	var managed strings.Builder
	managed.WriteString(gitignoreStart + "\n")
	for _, e := range entries {
		managed.WriteString(e + "\n")
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

func (s *Syncer) pruneStaleManaged(managedDirs map[string]bool) error {
	return filepath.WalkDir(s.Cfg.Root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && d.Name() == ".git" {
			return filepath.SkipDir
		}
		if d.IsDir() || d.Name() != ".gitignore" {
			return nil
		}
		relDir, err := filepath.Rel(s.Cfg.Root, filepath.Dir(path))
		if err != nil {
			return err
		}
		if managedDirs[relDir] {
			return nil
		}
		return removeManagedSection(path)
	})
}

func removeManagedSection(gitignorePath string) error {
	existing, err := os.ReadFile(gitignorePath)
	if err != nil {
		return err
	}
	content := string(existing)
	startIdx := strings.Index(content, gitignoreStart)
	endIdx := strings.Index(content, gitignoreEnd)
	if startIdx < 0 || endIdx <= startIdx {
		return nil
	}
	endIdx += len(gitignoreEnd)
	if endIdx < len(content) && content[endIdx] == '\n' {
		endIdx++
	}
	for startIdx > 0 && content[startIdx-1] == '\n' {
		startIdx--
	}
	result := content[:startIdx] + content[endIdx:]
	if strings.TrimSpace(result) == "" {
		return os.Remove(gitignorePath)
	}
	if !strings.HasSuffix(result, "\n") {
		result += "\n"
	}
	return os.WriteFile(gitignorePath, []byte(result), 0o644)
}
