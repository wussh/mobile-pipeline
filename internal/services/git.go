package services

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
)

type GitService struct {
	warp *WarpService

	// In-memory branch cache: project name → branch list
	// Populated from local .git on startup, updated after every fetch.
	// Reading from cache never needs network.
	cacheMu sync.RWMutex
	cache   map[string][]string
}

func NewGitService(warp *WarpService) *GitService {
	return &GitService{
		warp:  warp,
		cache: make(map[string][]string),
	}
}

// WarmCache reads branches from local .git without any network access.
// Call this on startup for each project that already has a local clone.
func (g *GitService) WarmCache(projectName, repoPath string) {
	if _, err := os.Stat(repoPath); err != nil {
		return // not cloned yet — cache stays empty
	}
	branches, err := g.readLocalBranches(repoPath)
	if err != nil || len(branches) == 0 {
		return
	}
	g.cacheMu.Lock()
	g.cache[projectName] = branches
	g.cacheMu.Unlock()
}

// ListBranches returns the cached branch list for a project.
// This is always served from local memory — no WARP toggle needed.
func (g *GitService) ListBranches(projectName string) []string {
	g.cacheMu.RLock()
	defer g.cacheMu.RUnlock()
	return g.cache[projectName]
}

// FetchLatest disconnects WARP, runs git fetch, updates cache, reconnects.
// Returns the updated branch list so callers get fresh data immediately.
func (g *GitService) FetchLatest(projectName, repoPath string, logFn func(string)) ([]string, error) {
	var branches []string

	err := g.warp.WithDisconnected(func() error {
		logFn("⚠️  WARP disconnected — fetching from Bitbucket...")

		_, err := g.git(repoPath, "fetch", "--all", "--prune")
		if err != nil {
			return fmt.Errorf("git fetch: %w", err)
		}

		logFn("✅ Fetch complete")

		// Read updated branch list from local .git while WARP is still off
		// (no network needed — git branch -r reads local refs)
		branches, err = g.readLocalBranches(repoPath)
		if err != nil {
			logFn(fmt.Sprintf("WARNING reading branches: %v", err))
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	// Update cache AFTER WARP is reconnected (warp.WithDisconnected has returned)
	if len(branches) > 0 {
		g.cacheMu.Lock()
		g.cache[projectName] = branches
		g.cacheMu.Unlock()
		logFn(fmt.Sprintf("Branch cache updated: %d branches", len(branches)))
	}

	logFn("✅ WARP reconnected — website back online")
	return branches, nil
}

// PullBranch checks out a branch and pulls latest.
// WARP is disconnected only for the network pull step.
func (g *GitService) PullBranch(repoPath, branch string, logFn func(string)) error {
	// Checkout is local — no network
	if _, err := g.git(repoPath, "checkout", branch); err != nil {
		return fmt.Errorf("git checkout: %w", err)
	}

	// Pull needs network — toggle WARP
	return g.warp.WithDisconnected(func() error {
		logFn("⚠️  WARP disconnected — pulling latest...")
		_, err := g.git(repoPath, "pull", "origin", branch)
		if err != nil {
			return fmt.Errorf("git pull: %w", err)
		}
		logFn("✅ Pull complete — WARP reconnecting...")
		return nil
	})
}

// CloneIfNotExists clones the repo if local_path doesn't exist.
// Always wrapped in WARP toggle.
func (g *GitService) CloneIfNotExists(projectName, repoURL, localPath string, logFn func(string)) error {
	if _, err := os.Stat(localPath); err == nil {
		return nil // already cloned
	}

	return g.warp.WithDisconnected(func() error {
		logFn(fmt.Sprintf("⚠️  WARP disconnected — cloning %s...", projectName))
		cmd := exec.Command("git", "clone", repoURL, localPath)
		var out bytes.Buffer
		cmd.Stdout = &out
		cmd.Stderr = &out
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("git clone: %w\n%s", err, out.String())
		}
		logFn("✅ Clone complete — WARP reconnecting...")
		return nil
	})
}

// readLocalBranches reads remote branch names from local .git/refs — no network.
func (g *GitService) readLocalBranches(repoPath string) ([]string, error) {
	out, err := g.git(repoPath, "branch", "-r")
	if err != nil {
		return nil, err
	}
	var branches []string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.Contains(line, "->") {
			continue
		}
		branch := strings.TrimPrefix(line, "origin/")
		branches = append(branches, branch)
	}
	return branches, nil
}

// git runs a git subcommand in repoPath and returns combined output.
func (g *GitService) git(repoPath string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = repoPath
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	return out.String(), err
}
