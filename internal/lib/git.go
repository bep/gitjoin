// Copyright 2026 Bjørn Erik Pedersen
// SPDX-License-Identifier: Apache-2.0

package lib

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type Repo struct {
	Path string
}

func (r Repo) IsGitRepo() bool {
	info, err := os.Stat(filepath.Join(r.Path, ".git"))
	return err == nil && info.IsDir()
}

func (r Repo) DefaultBranch() (string, error) {
	out, err := r.run("symbolic-ref", "refs/remotes/origin/HEAD")
	if err != nil {
		return "", err
	}
	parts := strings.Split(strings.TrimSpace(out), "/")
	if len(parts) == 0 {
		return "", fmt.Errorf("could not parse default branch")
	}
	return parts[len(parts)-1], nil
}

func (r Repo) CurrentBranch() (string, error) {
	out, err := r.run("branch", "--show-current")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

func (r Repo) HasUncommittedChanges() (bool, error) {
	out, err := r.run("status", "--porcelain")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) != "", nil
}

func (r Repo) ChangesSummary() string {
	out, _ := r.run("status", "--porcelain")
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return "no changes"
	}
	var modified, added, deleted int
	for _, line := range lines {
		if len(line) < 2 {
			continue
		}
		status := line[:2]
		if strings.Contains(status, "M") {
			modified++
		} else if strings.Contains(status, "A") || strings.Contains(status, "?") {
			added++
		} else if strings.Contains(status, "D") {
			deleted++
		}
	}
	var parts []string
	if modified > 0 {
		parts = append(parts, fmt.Sprintf("%d modified", modified))
	}
	if added > 0 {
		parts = append(parts, fmt.Sprintf("%d added", added))
	}
	if deleted > 0 {
		parts = append(parts, fmt.Sprintf("%d deleted", deleted))
	}
	if len(parts) == 0 {
		return fmt.Sprintf("%d changes", len(lines))
	}
	return strings.Join(parts, ", ")
}

func (r Repo) Pull() (changed bool, err error) {
	headBefore, err := r.run("rev-parse", "HEAD")
	if err != nil {
		return false, err
	}
	if _, err := r.run("pull"); err != nil {
		return false, err
	}
	headAfter, err := r.run("rev-parse", "HEAD")
	if err != nil {
		return false, err
	}
	return headBefore != headAfter, nil
}

func (r Repo) Stash() error {
	_, err := r.run("stash", "push", "-m", "gitjoin")
	return err
}

func (r Repo) Unstash() error {
	_, err := r.run("stash", "pop")
	return err
}

func (r Repo) SwitchBranch(branch string) error {
	_, err := r.run("switch", branch)
	return err
}

func (r Repo) run(args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = r.Path
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, stderr.String())
	}
	return stdout.String(), nil
}

func clone(url, path string, out io.Writer) error {
	cmd := exec.Command("git", "clone", url, path)
	cmd.Stdout = out
	cmd.Stderr = out
	return cmd.Run()
}
