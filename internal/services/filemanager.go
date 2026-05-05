package services

import (
	"crypto/hmac"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"ios-build-server/internal/models"

	"github.com/google/uuid"
)

type FileManager struct {
	buildDir  string
	uploadDir string
	expiry    time.Duration
	secret    string

	mu    sync.RWMutex
	files map[string]*models.FileRecord
}

func NewFileManager(buildDir, uploadDir string, expiry time.Duration, secret string) *FileManager {
	os.MkdirAll(buildDir, 0755)
	os.MkdirAll(uploadDir, 0755)
	return &FileManager{
		buildDir:  buildDir,
		uploadDir: uploadDir,
		expiry:    expiry,
		secret:    secret,
		files:     make(map[string]*models.FileRecord),
	}
}

// RegisterArtifact copies the built file into buildDir and creates a FileRecord.
func (fm *FileManager) RegisterArtifact(srcPath, project, branch, buildType string) (*models.FileRecord, error) {
	id := uuid.New().String()
	filename := fmt.Sprintf("%s_%s_%s_%s%s", project, branch, buildType, id[:8], filepath.Ext(srcPath))
	destPath := filepath.Join(fm.buildDir, filename)

	if err := copyFile(srcPath, destPath); err != nil {
		return nil, err
	}

	return fm.addRecord(id, filename, destPath)
}

// StoreUpload saves an uploaded file and creates a FileRecord.
func (fm *FileManager) StoreUpload(src io.Reader, filename string) (*models.FileRecord, error) {
	id := uuid.New().String()
	safeFilename := fmt.Sprintf("%s_%s", id[:8], filepath.Base(filename))
	destPath := filepath.Join(fm.uploadDir, safeFilename)

	f, err := os.Create(destPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if _, err := io.Copy(f, src); err != nil {
		return nil, err
	}

	return fm.addRecord(id, safeFilename, destPath)
}

func (fm *FileManager) addRecord(id, filename, path string) (*models.FileRecord, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}

	expiresAt := time.Now().Add(fm.expiry)
	token := fm.signToken(id, expiresAt)

	rec := &models.FileRecord{
		ID:          id,
		Filename:    filename,
		Size:        info.Size(),
		Path:        path,
		Token:       token,
		CreatedAt:   time.Now(),
		ExpiresAt:   expiresAt,
		DownloadURL: fmt.Sprintf("/download/%s?token=%s&expires=%d", id, token, expiresAt.Unix()),
	}

	fm.mu.Lock()
	fm.files[id] = rec
	fm.mu.Unlock()
	return rec, nil
}

func (fm *FileManager) GetFile(id string) (*models.FileRecord, bool) {
	fm.mu.RLock()
	defer fm.mu.RUnlock()
	f, ok := fm.files[id]
	return f, ok
}

func (fm *FileManager) ListFiles() []*models.FileRecord {
	fm.mu.RLock()
	defer fm.mu.RUnlock()
	out := make([]*models.FileRecord, 0, len(fm.files))
	for _, f := range fm.files {
		out = append(out, f)
	}
	return out
}

func (fm *FileManager) DeleteFile(id string) error {
	fm.mu.Lock()
	defer fm.mu.Unlock()
	f, ok := fm.files[id]
	if !ok {
		return fmt.Errorf("file not found")
	}
	os.Remove(f.Path)
	delete(fm.files, id)
	return nil
}

// ValidateToken checks HMAC signature and expiry.
func (fm *FileManager) ValidateToken(id, token string, expiresUnix int64) error {
	expiresAt := time.Unix(expiresUnix, 0)
	if time.Now().After(expiresAt) {
		return fmt.Errorf("link expired")
	}
	expected := fm.signToken(id, expiresAt)
	if !hmac.Equal([]byte(token), []byte(expected)) {
		return fmt.Errorf("invalid token")
	}
	return nil
}

func (fm *FileManager) signToken(id string, expiresAt time.Time) string {
	mac := hmac.New(sha256.New, []byte(fm.secret))
	mac.Write([]byte(id + strconv.FormatInt(expiresAt.Unix(), 10)))
	return fmt.Sprintf("%x", mac.Sum(nil))
}

// CleanExpired removes expired files from disk and memory.
func (fm *FileManager) CleanExpired() int {
	fm.mu.Lock()
	defer fm.mu.Unlock()
	count := 0
	for id, f := range fm.files {
		if time.Now().After(f.ExpiresAt) {
			os.Remove(f.Path)
			delete(fm.files, id)
			count++
		}
	}
	return count
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

// StartCleanup runs periodic cleanup of expired files.
func StartCleanup(fm *FileManager, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for range ticker.C {
		n := fm.CleanExpired()
		if n > 0 {
			fmt.Printf("[cleanup] removed %d expired file(s)\n", n)
		}
	}
}
