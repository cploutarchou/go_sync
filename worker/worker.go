// Package worker implements a worker pool to process tasks concurrently using goroutines.
//
// The worker pool consists of a set of worker goroutines that can process tasks concurrently.
// Tasks are represented by the Task struct, which includes an EventType indicating the type of event
// (e.g., creation, write, removal) and the Name of the file associated with the event.
//
// To use the worker pool, create a new Pool using NewWorkerPool, specifying the capacity of the pool,
// i.e., the maximum number of concurrent workers. Then, tasks can be submitted to the worker pool
// through the Tasks channel. Each worker goroutine in the pool will process tasks as they arrive.
// The worker pool ensures that tasks are processed in a concurrent and synchronized manner,
// allowing for efficient processing of multiple tasks simultaneously.
//
// Example usage:
//
//	// Create a worker pool with a capacity of 10 workers
//	pool := NewWorkerPool(10)
//
//	// Start the worker goroutines to process tasks
//	for i := 0; i < cap(pool.Tasks); i++ {
//	  go pool.Worker()
//	}
//
//	// Submit tasks to the worker pool
//	pool.Tasks <- Task{EventType: fsnotify.Create, Name: "file1.txt"}
//	pool.Tasks <- Task{EventType: fsnotify.Write, Name: "file2.txt"}
//	pool.Tasks <- Task{EventType: fsnotify.Remove, Name: "file3.txt"}
package worker

import (
	"sync"

	"github.com/fsnotify/fsnotify"
)

// Task represents a task that the WorkerPool operates on.
// It includes the EventType, indicating the type of file event (e.g., create, write, remove),
// and the Name, which is the file name associated with the event.
type Task struct {
	EventType fsnotify.Op
	Name      string
}

// Pool is a pool of worker goroutines that can process tasks concurrently.
type Pool struct {
	Tasks chan Task      // Tasks is the channel through which tasks are submitted to the worker pool.
	WG    sync.WaitGroup // WG is used to wait for all worker goroutines to finish their tasks.
}

// NewWorkerPool constructs a new WorkerPool with the given capacity.
// The capacity specifies the maximum number of concurrent workers in the pool.
func NewWorkerPool(capacity int) *Pool {
	return &Pool{
		Tasks: make(chan Task, capacity),
	}
}
