package ftp

import (
	"github.com/fsnotify/fsnotify"
)

// Worker starts a new worker goroutine.
func (f *FTP) Worker() {
	for task := range f.Pool.Tasks {
		switch task.EventType {
		case fsnotify.Create:
			switch f.Direction {
			case LocalToRemote:
				err := f.uploadFile(task.Name)
				if err != nil {
					logger.Println("Error uploading file:", err)
				}
			case RemoteToLocal:
				err := f.downloadFile(task.Name)
				if err != nil {
					logger.Println("Error downloading file:", err)
				}
			}
		case fsnotify.Write:
			err := f.uploadFile(task.Name)
			if err != nil {
				logger.Println("Error uploading file:", err)
			}
		case fsnotify.Remove:
			switch f.Direction {
			case LocalToRemote:
				err := f.removeRemoteFile(task.Name)
				if err != nil {
					logger.Println("Error deleting file:", err)
				}
			case RemoteToLocal:
				err := f.removeLocalFile(task.Name)
				if err != nil {
					logger.Println("Error removing remote file:", err)
				}
			}
		}
		f.Pool.WG.Done()
	}
}
