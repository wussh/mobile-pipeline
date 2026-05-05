package services

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

type GitService struct {
	warp *WarpService
}

func NewGitService(warp *WarpService) *GitService {
	return &GitService{warp: warp}
}

// ListBranches returns remote branch names from local cache (no network).
func (g *GitService) ListBranches(repoPath string) ([]string, error) {
	out, err := g.git(repoPath, "branch", "-r")
	if err != nil {
		return nil, err
	}
	var branches []string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		// Remove "origin/HEAD -> ..." entries
		if line == "" || strings.Contains(line, "->") {
			continue
		}
		// Strip "origin/" prefix
		branch := strings.TrimPrefix(line, "origin/")
		branches = append(branches, branch)
	}
	return branches, nil
}

// FetchLatest disconnects WARP, does git fetch, reconnects.
func (g *GitService) FetchLatest(repoPath string, logFn func(string)) error {
	return g.warp.WithDisconnected(func() error {
		logFn("⚠️  WARP disconnected — fetching from Bitbucket...")
		_, err := g.git(repoPath, "fetch", "--all", "--prune")
		if err != nil {
			return fmt.Errorf("git fetch: %w", err)
		}
		logFn("✅ Fetch complete — WARP reconnecting...")
		return nil
	})
}

// CheckoutBranch checks out a branch and pulls (WARP disconnected for pull).
func (g *GitService) CheckoutBranch(repoPath, branch string) error {
	// Checkout is local — no network needed
	if _, err := g.git(repoPath, "checkout", branch); err != nil {
		return fmt.Errorf("git checkout: %w", err)
	}
	// Pull needs network — toggle WARP
	return g.warp.WithDisconnected(func() error {
		_, err := g.git(repoPath, "pull", "origin", branch)
		return err
	})
}

// CloneIfNotExists clones the repo if local_path doesn't exist.
func (g *GitService) CloneIfNotExists(repoURL, localPath string) error {
	if _, err := os.Stat(localPath); err == nil {
		return nil // already exists
	}
	return g.warp.WithDisconnected(func() error {
		cmd := exec.Command("git", "clone", repoURL, localPath)
		var out bytes.Buffer
		cmd.Stdout = &out
		cmd.Stderr = &out
		return cmd.Run()
	})
}

// git runs a git command in the given repo directory.
func (g *GitService) git(repoPath string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = repoPath
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	return out.String(), err
}
