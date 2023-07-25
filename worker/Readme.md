Sure! Here's the content as Markdown:

```
# Worker Pool for Concurrent Task Processing

The `worker` package provides an efficient worker pool implementation to process tasks concurrently using goroutines. The pool consists of a set of worker goroutines that can handle tasks in a concurrent and synchronized manner. Tasks are represented by the `Task` struct, which includes an `EventType` indicating the type of event (e.g., creation, write, removal) and the `Name` of the file associated with the event.

## How to Use

To use the worker pool, follow these simple steps:

1. Import the package and the required dependencies:
```go
package main

import (
	"sync"
	"github.com/fsnotify/fsnotify"
	"github.com/cploutarchou/syncpkg/worker"
)
```

2. Create a new worker pool using `NewWorkerPool`, specifying the capacity of the pool (maximum number of concurrent workers):
```go
// Create a worker pool with a capacity of 10 workers
pool := worker.NewWorkerPool(10)
```

3. Start the worker goroutines to process tasks. In this example, we launch 10 worker goroutines:
```go
for i := 0; i < cap(pool.Tasks); i++ {
	go pool.Worker()
}
```

4. Submit tasks to the worker pool through the `Tasks` channel. Each worker goroutine will process tasks as they arrive:
```go
// Submit tasks to the worker pool
pool.Tasks <- worker.Task{EventType: fsnotify.Create, Name: "file1.txt"}
pool.Tasks <- worker.Task{EventType: fsnotify.Write, Name: "file2.txt"}
pool.Tasks <- worker.Task{EventType: fsnotify.Remove, Name: "file3.txt"}
```

## Example Usage

Here's an example of how you can use the worker pool:

```go
package main

import (
	"sync"
	"github.com/fsnotify/fsnotify"
	"github.com/cploutarchou/syncpkg/worker"
)

func main() {
	// Create a worker pool with a capacity of 10 workers
	pool := worker.NewWorkerPool(10)

	// Start the worker goroutines to process tasks
	for i := 0; i < cap(pool.Tasks); i++ {
		go pool.Worker()
	}

	// Submit tasks to the worker pool
	pool.Tasks <- worker.Task{EventType: fsnotify.Create, Name: "file1.txt"}
	pool.Tasks <- worker.Task{EventType: fsnotify.Write, Name: "file2.txt"}
	pool.Tasks <- worker.Task{EventType: fsnotify.Remove, Name: "file3.txt"}

	// Wait for all tasks to be completed
	pool.WG.Wait()
}
```

## License

This package is licensed under the MIT License - see the [LICENSE](https://raw.githubusercontent.com/cploutarchou/syncpkg/LICENCE) file for details.

The `worker` package empowers your Go applications to process tasks concurrently using a well-designed worker pool, ensuring efficient and synchronized handling of multiple tasks simultaneously.
