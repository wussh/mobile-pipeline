package handlers

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"ios-build-server/internal/config"
	"ios-build-server/internal/services"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

type Handler struct {
	cfg      *config.Config
	buildSvc *services.BuildService
	gitSvc   *services.GitService
	warpSvc  *services.WarpService
	fileMgr  *services.FileManager
}

func New(cfg *config.Config, buildSvc *services.BuildService, gitSvc *services.GitService, warpSvc *services.WarpService, fileMgr *services.FileManager) *Handler {
	return &Handler{cfg: cfg, buildSvc: buildSvc, gitSvc: gitSvc, warpSvc: warpSvc, fileMgr: fileMgr}
}

// ── Pages ──────────────────────────────────────────────────────────────────

func (h *Handler) IndexPage(c *gin.Context) {
	builds := h.buildSvc.ListBuilds()
	c.HTML(http.StatusOK, "index.html", gin.H{
		"Projects": h.cfg.Projects,
		"Builds":   builds,
	})
}

func (h *Handler) FilesPage(c *gin.Context) {
	files := h.fileMgr.ListFiles()
	c.HTML(http.StatusOK, "files.html", gin.H{
		"Files": files,
	})
}

// ── Projects & Branches ────────────────────────────────────────────────────

func (h *Handler) ListProjects(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"projects": h.cfg.Projects})
}

func (h *Handler) ListBranches(c *gin.Context) {
	project := c.Query("project")
	if h.findProject(project) == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "unknown project"})
		return
	}
	// Always served from in-memory cache — no WARP toggle needed
	branches := h.gitSvc.ListBranches(project)
	if branches == nil {
		branches = []string{}
	}
	c.JSON(http.StatusOK, gin.H{"branches": branches})
}

func (h *Handler) FetchBranches(c *gin.Context) {
	var body struct {
		Project string `json:"project"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	proj := h.findProject(body.Project)
	if proj == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "unknown project"})
		return
	}
	var logs []string
	branches, err := h.gitSvc.FetchLatest(body.Project, proj.LocalPath, func(line string) {
		logs = append(logs, line)
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error(), "log": logs})
		return
	}
	c.JSON(http.StatusOK, gin.H{"branches": branches, "log": logs})
}

func (h *Handler) PullLatest(c *gin.Context) {
	var body struct {
		Project string `json:"project"`
		Branch  string `json:"branch"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	proj := h.findProject(body.Project)
	if proj == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "unknown project"})
		return
	}
	if body.Branch == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "branch is required"})
		return
	}
	var logs []string
	if err := h.gitSvc.PullBranch(proj.LocalPath, body.Branch, func(line string) {
		logs = append(logs, line)
	}); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error(), "log": logs})
		return
	}
	c.JSON(http.StatusOK, gin.H{"log": logs})
}

// ── WARP ───────────────────────────────────────────────────────────────────

func (h *Handler) WarpStatus(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": h.warpSvc.Status()})
}

// ── Builds ─────────────────────────────────────────────────────────────────

func (h *Handler) TriggerBuild(c *gin.Context) {
	var body struct {
		Project   string `json:"project"`
		Branch    string `json:"branch"`
		BuildType string `json:"build_type"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	b, err := h.buildSvc.TriggerBuild(body.Project, body.Branch, body.BuildType)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusAccepted, b)
}

func (h *Handler) ListBuilds(c *gin.Context) {
	c.JSON(http.StatusOK, h.buildSvc.ListBuilds())
}

func (h *Handler) GetBuild(c *gin.Context) {
	b, ok := h.buildSvc.GetBuild(c.Param("id"))
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	c.JSON(http.StatusOK, b)
}

// ── WebSocket log stream ───────────────────────────────────────────────────

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

func (h *Handler) BuildLogWS(c *gin.Context) {
	buildID := c.Param("id")
	b, ok := h.buildSvc.GetBuild(buildID)
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "build not found"})
		return
	}

	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	// Send existing logs first
	for _, line := range b.LogOutput {
		conn.WriteMessage(websocket.TextMessage, []byte(line))
	}

	// If still running, subscribe for new lines
	if b.Status == "queued" || b.Status == "building" {
		ch := h.buildSvc.Subscribe(buildID)
		defer h.buildSvc.Unsubscribe(buildID, ch)
		for line := range ch {
			if err := conn.WriteMessage(websocket.TextMessage, []byte(line)); err != nil {
				return
			}
		}
	}
}

// ── File Upload / Download ─────────────────────────────────────────────────

func (h *Handler) UploadFile(c *gin.Context) {
	file, header, err := c.Request.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "no file"})
		return
	}
	defer file.Close()

	rec, err := h.fileMgr.StoreUpload(file, header.Filename)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	// Make URL absolute
	rec.DownloadURL = fmt.Sprintf("http://%s%s", c.Request.Host, rec.DownloadURL)
	c.JSON(http.StatusOK, rec)
}

func (h *Handler) ListFiles(c *gin.Context) {
	files := h.fileMgr.ListFiles()
	// Annotate with absolute URLs
	host := c.Request.Host
	for _, f := range files {
		f.DownloadURL = fmt.Sprintf("http://%s/download/%s?token=%s&expires=%d",
			host, f.ID, f.Token, f.ExpiresAt.Unix())
	}
	c.JSON(http.StatusOK, files)
}

func (h *Handler) DeleteFile(c *gin.Context) {
	if err := h.fileMgr.DeleteFile(c.Param("id")); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"deleted": true})
}

func (h *Handler) Download(c *gin.Context) {
	id := c.Param("id")
	token := c.Query("token")
	expiresStr := c.Query("expires")

	expiresUnix, err := strconv.ParseInt(expiresStr, 10, 64)
	if err != nil {
		c.String(http.StatusBadRequest, "invalid expires")
		return
	}

	if err := h.fileMgr.ValidateToken(id, token, expiresUnix); err != nil {
		c.String(http.StatusForbidden, err.Error())
		return
	}

	rec, ok := h.fileMgr.GetFile(id)
	if !ok {
		c.String(http.StatusNotFound, "file not found")
		return
	}

	c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, rec.Filename))
	c.File(rec.Path)
	_ = time.Now()
}

// ── helpers ────────────────────────────────────────────────────────────────

func (h *Handler) findProject(name string) *config.Project {
	for i := range h.cfg.Projects {
		if h.cfg.Projects[i].Name == name {
			return &h.cfg.Projects[i]
		}
	}
	return nil
}
