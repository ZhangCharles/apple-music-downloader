package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type Task struct {
	ID         string    `json:"id"`
	URL        string    `json:"url"`
	Output     string    `json:"output,omitempty"`
	Status     string    `json:"status"`
	Err        string    `json:"error,omitempty"`
	ResultDir  string    `json:"result_dir,omitempty"`
	Files      []string  `json:"files,omitempty"`
	ResultPath string    `json:"result_path,omitempty"`
	CleanupDir bool      `json:"cleanup_dir,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

const (
	defaultTaskTTL       = 24 * time.Hour
	cleanupCheckInterval = time.Hour
)

var (
	tasks   = make(map[string]*Task)
	tasksMu sync.RWMutex
	queue   chan *Task
)

func genID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func InitTaskQueue(workers int) {
	if workers <= 0 {
		workers = 1
	}
	queue = make(chan *Task, workers*4)
	for i := 0; i < workers; i++ {
		go worker(i)
	}
	go cleanupLoop(defaultTaskTTL, cleanupCheckInterval)
}

func EnqueueTask(url string, output string) *Task {
	t := &Task{
		ID:        genID(),
		URL:       url,
		Output:    output,
		Status:    "pending",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	tasksMu.Lock()
	tasks[t.ID] = t
	tasksMu.Unlock()
	queue <- t
	return t
}

func GetTask(id string) (*Task, bool) {
	tasksMu.RLock()
	defer tasksMu.RUnlock()
	t, ok := tasks[id]
	return t, ok
}

func cleanupLoop(ttl, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for range ticker.C {
		cleanupExpiredTasks(ttl)
	}
}

func cleanupExpiredTasks(ttl time.Duration) {
	now := time.Now()
	tasksMu.Lock()
	defer tasksMu.Unlock()
	for id, t := range tasks {
		if t.Status == "running" || t.Status == "pending" {
			continue
		}
		if now.Sub(t.UpdatedAt) <= ttl {
			continue
		}
		if t.CleanupDir && t.ResultDir != "" {
			if err := os.RemoveAll(t.ResultDir); err != nil {
				log.Printf("failed to cleanup task dir %s: %v", t.ResultDir, err)
			}
		}
		delete(tasks, id)
	}
}

func worker(i int) {
	for t := range queue {
		log.Printf("worker %d start task %s", i, t.ID)
		tasksMu.Lock()
		t.Status = "running"
		t.UpdatedAt = time.Now()
		tasksMu.Unlock()

		// prepare temp output dir
		tmpDir, err := ioutil.TempDir("", "amdl-task-")
		if err != nil {
			tasksMu.Lock()
			t.Status = "error"
			t.Err = fmt.Sprintf("failed to create tmpdir: %v", err)
			t.UpdatedAt = time.Now()
			tasksMu.Unlock()
			continue
		}

		// if caller provided output dir, use it by passing --output
		exe, _ := os.Executable()
		args := []string{"--output", tmpDir, t.URL}
		if t.Output != "" {
			args = []string{"--output", t.Output, t.URL}
		}
		cmd := exec.Command(exe, args...)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			tasksMu.Lock()
			t.Status = "error"
			t.Err = fmt.Sprintf("downloader failed: %v", err)
			t.UpdatedAt = time.Now()
			tasksMu.Unlock()
			_ = os.RemoveAll(tmpDir)
			continue
		}

		outputDir := tmpDir
		if t.Output != "" {
			outputDir = t.Output
		}

		files := []string{}
		err = filepath.WalkDir(outputDir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() {
				files = append(files, path)
			}
			return nil
		})
		if err != nil || len(files) == 0 {
			tasksMu.Lock()
			t.Status = "error"
			if err != nil {
				t.Err = fmt.Sprintf("failed to inspect output: %v", err)
			} else {
				t.Err = "no output file produced"
			}
			t.UpdatedAt = time.Now()
			tasksMu.Unlock()
			if t.Output == "" {
				_ = os.RemoveAll(tmpDir)
			}
			continue
		}

		// move result files to a stable location when using a temp workspace
		if t.Output == "" {
			destDir, _ := ioutil.TempDir("", "amdl-result-")
			os.MkdirAll(destDir, 0755)
			for _, src := range files {
				relPath, err := filepath.Rel(outputDir, src)
				if err != nil {
					continue
				}
				dst := filepath.Join(destDir, relPath)
				if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
					continue
				}
				if err := os.Rename(src, dst); err != nil {
					data, err2 := ioutil.ReadFile(src)
					if err2 == nil {
						_ = ioutil.WriteFile(dst, data, 0644)
					}
				}
			}
			_ = os.RemoveAll(outputDir)
			t.ResultDir = destDir
		} else {
			t.ResultDir = outputDir
		}
		t.Files = make([]string, 0, len(files))
		for _, f := range files {
			t.Files = append(t.Files, filepath.ToSlash(filepath.Clean(strings.TrimPrefix(f, outputDir+string(os.PathSeparator)))))
		}

		tasksMu.Lock()
		t.Status = "done"
		t.ResultPath = t.ResultDir
		t.UpdatedAt = time.Now()
		tasksMu.Unlock()
		// cleanup tmpDir only when the downloader used it
		if t.Output == "" {
			_ = os.RemoveAll(tmpDir)
		}
		log.Printf("worker %d finish task %s -> %s", i, t.ID, t.ResultDir)
	}
}

// helper to write task as json to writer
func writeJSON(w io.Writer, v interface{}) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
