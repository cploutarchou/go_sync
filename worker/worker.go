package worker

import (
	"log"
	"sync"

	"github.com/fsnotify/fsnotify"
)

// Task struct for Pool to operate on
type Task struct {
	EventType fsnotify.Op
	Name      string
	Func      func() error
}

type Pool struct {
	Tasks chan Task
	WG    sync.WaitGroup
}

func NewWorkerPool(numWorkers int) *Pool {
	pool := &Pool{
		Tasks: make(chan Task),
	}

	pool.WG.Add(numWorkers)
	for i := 0; i < numWorkers; i++ {
		go pool.worker()
	}

	return pool
}

func (p *Pool) Submit(taskFunc func() error) error {
	p.Tasks <- Task{Func: taskFunc}
	return nil
}

func (p *Pool) worker() {
	for task := range p.Tasks {
		err := task.Func()
		if err != nil {
			log.Println("Error executing task:", err)
		}
		p.WG.Done()
	}
}
