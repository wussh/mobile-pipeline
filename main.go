package main

import (
	"fmt"
	"html/template"
	"log"

	"ios-build-server/internal/config"
	"ios-build-server/internal/handlers"
	"ios-build-server/internal/services"

	"github.com/gin-gonic/gin"
)

func main() {
	cfg, err := config.Load("config.yaml")
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}

	// Ensure directories exist
	// (FileManager.New handles this)

	// Init services
	fileMgr := services.NewFileManager(cfg.BuildOutputDir, cfg.UploadDir, cfg.LinkExpiry, cfg.SecretKey)
	warpSvc := services.NewWarpService()
	gitSvc := services.NewGitService(warpSvc)
	buildSvc := services.NewBuildService(cfg, gitSvc, fileMgr)

	// Pre-populate branch cache from local .git (no network needed)
	for _, proj := range cfg.Projects {
		gitSvc.WarmCache(proj.Name, proj.LocalPath)
	}
	log.Printf("Branch cache warmed for %d project(s)", len(cfg.Projects))

	// Start cleanup goroutine
	go services.StartCleanup(fileMgr, cfg.CleanupInterval)

	// Router
	r := gin.Default()

	// Register template function
	r.SetFuncMap(template.FuncMap{
		"formatBytes": formatBytes,
		"slice": func(s string, start, end int) string {
			if end > len(s) {
				end = len(s)
			}
			return s[start:end]
		},
	})
	r.LoadHTMLGlob("web/templates/*")

	// Basic auth
	auth := gin.BasicAuth(gin.Accounts{cfg.AuthUser: cfg.AuthPass})

	h := handlers.New(cfg, buildSvc, gitSvc, warpSvc, fileMgr)

	// Pages
	r.GET("/", auth, h.IndexPage)
	r.GET("/files", auth, h.FilesPage)

	// API (protected)
	api := r.Group("/api", auth)
	{
		api.GET("/projects", h.ListProjects)
		api.GET("/branches", h.ListBranches)
		api.POST("/fetch", h.FetchBranches)
		api.POST("/pull", h.PullLatest)
		api.GET("/warp/status", h.WarpStatus)

		api.POST("/build", h.TriggerBuild)
		api.GET("/builds", h.ListBuilds)
		api.GET("/builds/:id", h.GetBuild)

		api.POST("/upload", h.UploadFile)
		api.GET("/files", h.ListFiles)
		api.DELETE("/files/:id", h.DeleteFile)
	}

	// WebSocket (basic auth skipped — uses build ID as implicit auth)
	r.GET("/ws/build/:id", h.BuildLogWS)

	// Download (token-validated, no basic auth)
	r.GET("/download/:id", h.Download)

	log.Printf("Server starting on :%d", cfg.Port)
	if err := r.Run(fmt.Sprintf(":%d", cfg.Port)); err != nil {
		log.Fatalf("server error: %v", err)
	}
}

func formatBytes(b int64) string {
	switch {
	case b < 1024:
		return fmt.Sprintf("%d B", b)
	case b < 1024*1024:
		return fmt.Sprintf("%.1f KB", float64(b)/1024)
	default:
		return fmt.Sprintf("%.1f MB", float64(b)/1024/1024)
	}
}
