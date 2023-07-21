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

// Pool is a pool of workers that run tasks
type Pool struct {
	Tasks chan Task
	WG    sync.WaitGroup
}

// NewWorkerPool constructs a new WorkerPool of a given size
func NewWorkerPool(capacity int) *Pool {
	return &Pool{
		Tasks: make(chan Task, capacity),
	}
}