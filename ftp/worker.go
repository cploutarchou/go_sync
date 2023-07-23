package ftp

import (
	"github.com/fsnotify/fsnotify"
)

// Worker starts a new worker goroutine.
func (f *FTP) Worker() {
	defer f.Pool.WG.Done()
	for task := range f.Pool.Tasks {
		logger.Println("Processing task:", task)
		switch task.EventType {
		case fsnotify.Write:
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
		case fsnotify.Remove:
			switch f.Direction {
			case LocalToRemote:
				err := f.removeRemoteFile(task.Name)
				if err != nil {
					logger.Println("Error removing remote file:", err)
				}
			case RemoteToLocal:
				err := f.removeLocalFile(task.Name)
				if err != nil {
					logger.Println("Error removing local file:", err)
				}
			}
		case fsnotify.Rename:
			switch f.Direction {
			case LocalToRemote:
				err := f.uploadFile(task.Name)
				if err != nil {
					logger.Println("Error uploading file:", err)
				}
				err = f.removeRemoteFile(task.Name)
				if err != nil {
					logger.Println("Error removing remote file:", err)
				}
			case RemoteToLocal:
				err := f.downloadFile(task.Name)
				if err != nil {
					logger.Println("Error downloading file:", err)
				}
				err = f.removeLocalFile(task.Name)
				if err != nil {
					logger.Println("Error removing local file:", err)
				}
			}
		case fsnotify.Chmod:
			logger.Println("Permissions of file changed:", task.Name)
		}
		f.Pool.WG.Done()
	}
}
