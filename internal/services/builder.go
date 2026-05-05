package services

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"ios-build-server/internal/config"
	"ios-build-server/internal/models"

	"github.com/google/uuid"
)

type BuildService struct {
	cfg     *config.Config
	git     *GitService
	fileMgr *FileManager

	mu     sync.RWMutex
	builds map[string]*models.Build

	queue chan *models.Build

	// subscribers for live log streaming: buildID -> list of channels
	subMu sync.RWMutex
	subs  map[string][]chan string
}

func NewBuildService(cfg *config.Config, git *GitService, fileMgr *FileManager) *BuildService {
	bs := &BuildService{
		cfg:     cfg,
		git:     git,
		fileMgr: fileMgr,
		builds:  make(map[string]*models.Build),
		queue:   make(chan *models.Build, 10),
		subs:    make(map[string][]chan string),
	}
	go bs.worker()
	return bs
}

func (bs *BuildService) TriggerBuild(project, branch, buildType string) (*models.Build, error) {
	// Validate project
	var proj *config.Project
	for i := range bs.cfg.Projects {
		if bs.cfg.Projects[i].Name == project {
			proj = &bs.cfg.Projects[i]
			break
		}
	}
	if proj == nil {
		return nil, fmt.Errorf("unknown project: %s", project)
	}
	if buildType != "ipa" && buildType != "apk" {
		return nil, fmt.Errorf("invalid build type: %s (must be ipa or apk)", buildType)
	}

	b := &models.Build{
		ID:        uuid.New().String(),
		Project:   project,
		Branch:    branch,
		BuildType: buildType,
		Status:    models.StatusQueued,
		StartedAt: time.Now(),
	}

	bs.mu.Lock()
	bs.builds[b.ID] = b
	bs.mu.Unlock()

	bs.queue <- b
	return b, nil
}

func (bs *BuildService) GetBuild(id string) (*models.Build, bool) {
	bs.mu.RLock()
	defer bs.mu.RUnlock()
	b, ok := bs.builds[id]
	return b, ok
}

func (bs *BuildService) ListBuilds() []*models.Build {
	bs.mu.RLock()
	defer bs.mu.RUnlock()
	out := make([]*models.Build, 0, len(bs.builds))
	for _, b := range bs.builds {
		out = append(out, b)
	}
	return out
}

// Subscribe returns a channel that receives log lines for a build.
func (bs *BuildService) Subscribe(buildID string) chan string {
	ch := make(chan string, 100)
	bs.subMu.Lock()
	bs.subs[buildID] = append(bs.subs[buildID], ch)
	bs.subMu.Unlock()
	return ch
}

func (bs *BuildService) Unsubscribe(buildID string, ch chan string) {
	bs.subMu.Lock()
	defer bs.subMu.Unlock()
	subs := bs.subs[buildID]
	for i, s := range subs {
		if s == ch {
			bs.subs[buildID] = append(subs[:i], subs[i+1:]...)
			break
		}
	}
	close(ch)
}

func (bs *BuildService) publish(buildID, line string) {
	bs.subMu.RLock()
	defer bs.subMu.RUnlock()
	for _, ch := range bs.subs[buildID] {
		select {
		case ch <- line:
		default:
		}
	}
}

// worker processes builds one at a time.
func (bs *BuildService) worker() {
	for b := range bs.queue {
		bs.runBuild(b)
	}
}

func (bs *BuildService) runBuild(b *models.Build) {
	log := func(line string) {
		bs.mu.Lock()
		b.LogOutput = append(b.LogOutput, line)
		bs.mu.Unlock()
		bs.publish(b.ID, line)
	}

	setStatus := func(s models.BuildStatus) {
		bs.mu.Lock()
		b.Status = s
		bs.mu.Unlock()
	}

	setStatus(models.StatusBuilding)
	log(fmt.Sprintf("=== Build started: project=%s branch=%s type=%s ===", b.Project, b.Branch, b.BuildType))

	// Find project config
	var proj *config.Project
	for i := range bs.cfg.Projects {
		if bs.cfg.Projects[i].Name == b.Project {
			proj = &bs.cfg.Projects[i]
		}
	}

	// Clone if needed
	log(fmt.Sprintf("Checking repo at %s...", proj.LocalPath))
	if err := bs.git.CloneIfNotExists(proj.RepoURL, proj.LocalPath); err != nil {
		log(fmt.Sprintf("ERROR clone: %v", err))
		setStatus(models.StatusFailed)
		return
	}

	// Checkout branch (includes WARP toggle for pull)
	log(fmt.Sprintf("Checking out branch: %s", b.Branch))
	if err := bs.git.CheckoutBranch(proj.LocalPath, b.Branch); err != nil {
		log(fmt.Sprintf("ERROR checkout: %v", err))
		setStatus(models.StatusFailed)
		return
	}

	// Run fvm flutter build
	var buildArgs []string
	if b.BuildType == "ipa" {
		buildArgs = []string{"flutter", "build", "ipa", "--release"}
	} else {
		buildArgs = []string{"flutter", "build", "apk", "--release"}
	}

	log(fmt.Sprintf("Running: fvm %s", strings.Join(buildArgs, " ")))
	cmd := exec.Command("fvm", buildArgs...)
	cmd.Dir = proj.LocalPath

	stdout, _ := cmd.StdoutPipe()
	stderr, _ := cmd.StderrPipe()

	if err := cmd.Start(); err != nil {
		log(fmt.Sprintf("ERROR start build: %v", err))
		setStatus(models.StatusFailed)
		return
	}

	// Stream stdout + stderr
	streamReader := func(r io.Reader) {
		scanner := bufio.NewScanner(r)
		for scanner.Scan() {
			log(scanner.Text())
		}
	}
	go streamReader(stdout)
	go streamReader(stderr)

	err := cmd.Wait()
	if err != nil {
		log(fmt.Sprintf("ERROR build failed: %v", err))
		setStatus(models.StatusFailed)
		t := time.Now()
		bs.mu.Lock()
		b.EndedAt = &t
		bs.mu.Unlock()
		return
	}

	// Find artifact
	artifactPath, err := findArtifact(proj.LocalPath, b.BuildType)
	if err != nil {
		log(fmt.Sprintf("WARNING artifact not found: %v", err))
	} else {
		log(fmt.Sprintf("Artifact found: %s", artifactPath))
		rec, err := bs.fileMgr.RegisterArtifact(artifactPath, b.Project, b.Branch, b.BuildType)
		if err != nil {
			log(fmt.Sprintf("WARNING failed to register artifact: %v", err))
		} else {
			bs.mu.Lock()
			b.Artifact = rec
			bs.mu.Unlock()
			log(fmt.Sprintf("Download URL: %s", rec.DownloadURL))
		}
	}

	setStatus(models.StatusSuccess)
	t := time.Now()
	bs.mu.Lock()
	b.EndedAt = &t
	bs.mu.Unlock()
	log("=== Build complete ===")
}

func findArtifact(repoPath, buildType string) (string, error) {
	var searchDir, ext string
	if buildType == "ipa" {
		searchDir = filepath.Join(repoPath, "build", "ios", "ipa")
		ext = ".ipa"
	} else {
		searchDir = filepath.Join(repoPath, "build", "app", "outputs", "flutter-apk")
		ext = ".apk"
	}
	entries, err := os.ReadDir(searchDir)
	if err != nil {
		return "", err
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ext) {
			return filepath.Join(searchDir, e.Name()), nil
		}
	}
	return "", fmt.Errorf("no %s found in %s", ext, searchDir)
}
