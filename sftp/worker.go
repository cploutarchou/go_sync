package sftp

import (
	"os"

	"github.com/fsnotify/fsnotify"
)

// worker is a function for workers to consume tasks and handle them
func (s *SFTP) worker() {
	for task := range s.Pool.Tasks {
		switch task.EventType {
		case fsnotify.Write:
			// If the event is a write event, we either upload or download the file
			// depending on the sync direction.
			if s.Direction == LocalToRemote {
				err := s.uploadFile(task.Name)
				if err != nil {
					logger.Println("Error uploading file:", err)
				}
			} else if s.Direction == RemoteToLocal {
				err := s.downloadFile(task.Name)
				if err != nil {
					logger.Println("Error downloading file:", err)
				}
			}
		case fsnotify.Remove:
			// If the event is a remove event, we either remove the remote file
			// or the local file depending on the sync direction.
			if s.Direction == LocalToRemote {
				path := s.config.RemoteDir + task.Name[len(s.config.LocalDir):]
				err := s.RemoveRemoteFile(path)
				if err != nil {
					logger.Println("Error removing remote file:", err)
				}
			} else if s.Direction == RemoteToLocal {
				err := s.RemoveLocalFile(task.Name)
				if err != nil {
					logger.Println("Error removing local file:", err)
				}
			}
		}
		s.Pool.WG.Done()
	}
}

// watcherWorker is a worker for handling fsnotify events
func (s *SFTP) watcherWorker(workerId int, events <-chan fsnotify.Event) {
	for {
		select {
		case event := <-events:
			logger.Printf("Worker %d received event: %s", workerId, event)

			// Handling create events
			if event.Op&fsnotify.Create == fsnotify.Create {
				info, err := os.Stat(event.Name)
				if err == nil && info.IsDir() {
					err = s.Watcher.Add(event.Name)
					if err != nil {
						logger.Println("Error adding directory to watcher:", err)
					} else {
						logger.Println("Adding new directory to watcher:", event.Name)
					}
				}
			}

			// Handling write events
			if event.Op&fsnotify.Write == fsnotify.Write {
				logger.Println("Modified file:", event.Name)
				if s.Direction == LocalToRemote {
					err := s.uploadFile(event.Name)
					if err != nil {
						logger.Println("Error uploading file:", err)
					}
				}
				if s.Direction == RemoteToLocal {
					err := s.downloadFile(event.Name)
					if err != nil {
						logger.Println("Error downloading file:", err)
					}
				}
			}

			// Handling remove events
			if event.Op&fsnotify.Remove == fsnotify.Remove {
				logger.Println("Deleted file:", event.Name)
				if s.Direction == LocalToRemote {
					path := s.config.RemoteDir + event.Name[len(s.config.LocalDir):]
					err := s.RemoveRemoteFile(path)
					if err != nil {
						logger.Println("Error removing remote file:", err)
					}
				}
				if s.Direction == RemoteToLocal {
					err := s.RemoveLocalFile(event.Name)
					if err != nil {
						logger.Println("Error removing local file:", err)
					}
				}
			}

		case <-s.ctx.Done():
			// Stopping the worker if the context is done
			logger.Printf("Worker %d stopping", workerId)
			return
		}
	}
}
