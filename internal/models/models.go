package models

import "time"

type BuildStatus string

const (
	StatusQueued   BuildStatus = "queued"
	StatusBuilding BuildStatus = "building"
	StatusSuccess  BuildStatus = "success"
	StatusFailed   BuildStatus = "failed"
)

type Build struct {
	ID        string      `json:"id"`
	Project   string      `json:"project"`    // "mbtw" or "mbtl"
	Branch    string      `json:"branch"`
	BuildType string      `json:"build_type"` // "ipa" or "apk"
	Status    BuildStatus `json:"status"`
	StartedAt time.Time   `json:"started_at"`
	EndedAt   *time.Time  `json:"ended_at"`
	LogOutput []string    `json:"log_output"`
	Artifact  *FileRecord `json:"artifact,omitempty"`
}

type FileRecord struct {
	ID          string    `json:"id"`
	Filename    string    `json:"filename"`
	Size        int64     `json:"size"`
	Path        string    `json:"-"`
	DownloadURL string    `json:"download_url"`
	Token       string    `json:"token"`
	CreatedAt   time.Time `json:"created_at"`
	ExpiresAt   time.Time `json:"expires_at"`
	Downloaded  int       `json:"downloaded"`
}
