package worker

import (
	"sync"

	"github.com/fsnotify/fsnotify"
)

// Task struct for WorkerPool to operate on
type Task struct {
	EventType fsnotify.Op
	Name      string
}

// WorkerPool is a pool of workers that run tasks
type WorkerPool struct {
	Tasks chan Task
	WG   sync.WaitGroup
}

// NewWorkerPool constructs a new WorkerPool of a given size
func NewWorkerPool(size int) *WorkerPool {
	return &WorkerPool{
		Tasks: make(chan Task, size),
	}
}
