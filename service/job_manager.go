package service

import (
	"context"
	"crypto/rand"
	"fmt"
	"log/slog"
	"runtime"
	"sync"
	"time"
)

// JobStatus defines the possible states of a background job.
type JobStatus string

const (
	StatusPending    JobStatus = "PENDING"
	StatusProcessing JobStatus = "PROCESSING"
	StatusCompleted  JobStatus = "COMPLETED"
	StatusFailed     JobStatus = "FAILED"
)

// Job represents a background task tracked by the API.
type Job struct {
	ID        string      `json:"job_id"`
	Status    JobStatus   `json:"status"`
	Result    any         `json:"result,omitempty"`
	Error     string      `json:"error,omitempty"`
	CreatedAt time.Time   `json:"created_at"`
	UpdatedAt time.Time   `json:"updated_at"`
}

var (
	jobsMutex sync.RWMutex
	jobs      = make(map[string]*Job)
)

// GenerateJobID creates a unique UUID-v4 style string for job tracking.
func GenerateJobID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

// CreateJob initializes a new job in the in-memory registry.
func CreateJob() string {
	id := GenerateJobID()
	jobsMutex.Lock()
	jobs[id] = &Job{
		ID:        id,
		Status:    StatusPending,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	jobsMutex.Unlock()
	return id
}

// UpdateJobStatus updates the state, result, or error of an existing job.
func UpdateJobStatus(id string, status JobStatus, result any, errStr string) {
	jobsMutex.Lock()
	defer jobsMutex.Unlock()
	if job, exists := jobs[id]; exists {
		job.Status = status
		job.Result = result
		job.Error = errStr
		job.UpdatedAt = time.Now()
	}
}

// GetJob retrieves a job from the registry by its ID.
func GetJob(id string) (*Job, bool) {
	jobsMutex.RLock()
	defer jobsMutex.RUnlock()
	job, exists := jobs[id]
	return job, exists
}

// Task represents a unit of work that can be enqueued for background processing.
type Task struct {
	JobID       string
	ExecuteFunc func(ctx context.Context) (any, error)
}

var taskQueue chan Task

// InitWorkerPool starts background workers to process the task queue.
// The number of workers matches the number of CPU cores for optimal bounded concurrency.
func InitWorkerPool() {
	// Queue size handle bursts of requests
	taskQueue = make(chan Task, 1024)

	// Create workers based on CPU core count
	workerCount := runtime.NumCPU()
	if workerCount < 2 {
		workerCount = 2
	}

	for i := 0; i < workerCount; i++ {
		go worker(i)
	}
	slog.Info("Worker pool initialized", "workers", workerCount)
}

func worker(id int) {
	for task := range taskQueue {
		slog.Info("Worker started job", "worker_id", id, "job_id", task.JobID)
		UpdateJobStatus(task.JobID, StatusProcessing, nil, "")

		// Enforce a hard maximum lifetime for tasks to prevent worker leaks
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		result, err := task.ExecuteFunc(ctx)
		cancel()

		if err != nil {
			UpdateJobStatus(task.JobID, StatusFailed, nil, err.Error())
			slog.Error("Worker job failed", "worker_id", id, "job_id", task.JobID, "error", err)
		} else {
			UpdateJobStatus(task.JobID, StatusCompleted, result, "")
			slog.Info("Worker finished job successfully", "worker_id", id, "job_id", task.JobID)
		}
	}
}

// EnqueueTask adds a new task to the background processing queue.
func EnqueueTask(task Task) {
	taskQueue <- task
}
