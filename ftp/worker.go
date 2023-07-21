package ftp

import (
	"fmt"

	"github.com/fsnotify/fsnotify"
)

// Worker starts a new worker goroutine.
func (s *FTP) Worker() {
	for task := range s.Pool.Tasks {
		switch task.EventType {
		case fsnotify.Create:
			switch s.Direction {
			case LocalToRemote:
				fmt.Println("Uploading file:", task.Name)
				err := s.uploadFile(task.Name)
				if err != nil {
					logger.Println("Error uploading file:", err)
				}
			case RemoteToLocal:
				err := s.downloadFile(task.Name)
				if err != nil {
					logger.Println("Error downloading file:", err)
				}
			}
		case fsnotify.Write:
			err := s.uploadFile(task.Name)
			if err != nil {
				logger.Println("Error uploading file:", err)
			}
		case fsnotify.Remove:
			switch s.Direction {
			case LocalToRemote:
				err := s.removeRemoteFile(task.Name)
				if err != nil {
					logger.Println("Error deleting file:", err)
				}
			case RemoteToLocal:
				err := s.removeLocalFile(task.Name)
				if err != nil {
					logger.Println("Error removing remote file:", err)
				}
			}
		}
		s.Pool.WG.Done()
	}
}
