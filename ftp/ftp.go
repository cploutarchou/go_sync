package ftp

import (
	"context"
	"fmt"
	"github.com/secsy/goftp"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/cploutarchou/syncpkg/worker"
	"github.com/fsnotify/fsnotify"
)

var logger = log.New(os.Stdout, "ftp: ", log.Lshortfile)

type SyncDirection int

const (
	LocalToRemote SyncDirection = iota
	RemoteToLocal
)

type FTP struct {
	sync.Mutex
	client    *goftp.Client
	Direction SyncDirection
	config    *ExtraConfig
	Watcher   *fsnotify.Watcher
	Pool      *worker.Pool
	ctx       context.Context
}

type ExtraConfig struct {
	Username   string
	Password   string
	LocalDir   string
	RemoteDir  string
	Retries    int
	MaxRetries int
}

func Connect(address string, port int, direction SyncDirection, config *ExtraConfig) (*FTP, error) {
	address = fmt.Sprintf("%s:%d", address, port)

	ftpConfig := goftp.Config{
		User:     config.Username,
		Password: config.Password,
	}

	client, err := goftp.DialConfig(ftpConfig, address)
	if err != nil {
		return nil, err
	}

	ftp := &FTP{
		client:    client,
		Direction: direction,
		ctx:       context.Background(),
		Pool:      worker.NewWorkerPool(10),
	}
	ftp.config = config

	logger.Println("Connected to FTP server.")
	return ftp, nil
}
func (f *FTP) List(path string) ([]string, error) {
	files, err := f.client.ReadDir(path)
	if err != nil {
		return nil, err
	}

	var fileNames []string
	for _, file := range files {
		fileNames = append(fileNames, file.Name())
	}

	return fileNames, nil
}

func (f *FTP) initialSync() error {
	return f.syncDir(f.config.LocalDir, f.config.RemoteDir)
}

func (f *FTP) syncDir(localDir, remoteDir string) error {
	logger.Println("syncDir localDir", localDir)
	switch f.Direction {
	case LocalToRemote:
		localFiles, err := os.ReadDir(localDir)
		if err != nil {
			return err
		}
		for _, file := range localFiles {
			localFilePath := filepath.Join(localDir, file.Name())
			remoteFilePath := filepath.Join(remoteDir, file.Name())
			if file.IsDir() {
				err = f.checkOrCreateDir(remoteFilePath)
				if err != nil {
					return err
				}
				err = f.syncDir(localFilePath, remoteFilePath)
				if err != nil {
					return err
				}
			} else {
				// stat remote file and if it doesn't exist upload it to the server
				_, err = f.client.Stat(remoteFilePath)
				if err != nil {
					localFile, err := os.Open(localFilePath)
					if err != nil {
						return err
					}
					defer func(localFile *os.File) {
						_ = localFile.Close()
					}(localFile)
					err = f.client.Store(remoteFilePath, localFile)
					if err != nil {
						return err
					}
				}
			}
		}
	case RemoteToLocal:
		// Read the remote directory and all subdirectories.
		remoteFiles, err := f.client.ReadDir(remoteDir)
		if err != nil {
			return err
		}
		for _, file := range remoteFiles {
			remoteFilePath := filepath.Join(remoteDir, file.Name())
			localFilePath := filepath.Join(localDir, file.Name())
			if file.IsDir() {
				err = f.checkOrCreateDir(localFilePath)
				if err != nil {
					return err
				}
				err = f.syncDir(localFilePath, remoteFilePath)
				if err != nil {
					return err
				}
			} else {
				// stat local file and if it doesn't exist download it from the server
				_, err = os.Stat(localFilePath)
				if os.IsNotExist(err) {
					localFile, err := os.Create(localFilePath)
					if err != nil {
						return err
					}
					defer func(localFile *os.File) {
						_ = localFile.Close()
					}(localFile)
					err = f.client.Retrieve(remoteFilePath, localFile)
					if err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}

func (f *FTP) WatchDirectory() {
	// Starting the worker pool
	for i := 0; i < cap(f.Pool.Tasks); i++ {
		go f.Worker()
	}
	logger.Println("Starting initial sync...")
	err := f.initialSync()
	if err != nil {
		logger.Fatal(err)
	}
	logger.Println("Initial sync done.")

	logger.Println("Setting up watcher...")
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		logger.Fatal(err)
	}
	defer func(watcher *fsnotify.Watcher) {
		_ = watcher.Close()
	}(watcher) // Moved defer to here.

	go func() {
		for {
			select {
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				logger.Println("Received event:", event)

				f.Pool.WG.Add(1)
				f.Pool.Tasks <- worker.Task{EventType: event.Op, Name: event.Name}
			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				logger.Println("Error:", err)
			}
		}
	}()

	// Add root directory and all subdirectories to the watcher
	err = f.AddDirectoriesToWatcher(watcher, f.config.LocalDir)
	if err != nil {
		logger.Fatal(err)
	}

	<-f.ctx.Done()
	logger.Println("Directory watch ended.")
}

func (f *FTP) uploadFile(filePath string) error {
	// Open the file for reading
	file, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer func(file *os.File) {
		_ = file.Close()
	}(file)

	// Try to upload the file for MaxRetries times
	for i := 0; i < f.config.MaxRetries; i++ {
		// Calculate the remote file path
		correctedFilePath := strings.Replace(filePath, f.config.LocalDir, "", 1)
		correctedFilePath = filepath.Join(f.config.RemoteDir, correctedFilePath)

		// Reset the file pointer to the beginning of the file
		_, err = file.Seek(0, 0)
		if err != nil {
			return err
		}

		// Upload the file to the FTP server
		err = f.client.Store(correctedFilePath, file)
		if err != nil {
			// If upload fails, log the error and try again
			logger.Printf("Attempt %d/%d: Error uploading file: %v", i+1, f.config.MaxRetries, err)
			continue
		} else {
			// If upload succeeds, log the success and return nil
			logger.Printf("Uploaded file: %s", filePath)
			return nil
		}
	}

	// If we reach this point, all attempts to upload the file have failed
	return fmt.Errorf("failed to upload file after %d attempts", f.config.MaxRetries)
}

func (f *FTP) downloadFile(name string) error {
	f.Lock()
	defer f.Unlock()

	// Create the local file
	file, err := os.Create(filepath.Join(f.config.LocalDir, name))
	if err != nil {
		return err
	}
	defer func(file *os.File) {
		_ = file.Close()
	}(file)

	for i := 0; i < f.config.MaxRetries; i++ {
		// Calculate the remote file path
		remotePath := filepath.Join(f.config.RemoteDir, name)

		// Download the file from the FTP server
		err = f.client.Retrieve(remotePath, file)
		if err != nil {
			// If download fails, log the error and try again
			logger.Printf("Attempt %d/%d: Error downloading file: %v", i+1, f.config.MaxRetries, err)
			continue
		} else {
			// If download succeeds, log the success and return nil
			logger.Printf("Downloaded file: %s", name)
			return nil
		}
	}

	// If we reach this point, all attempts to download the file have failed
	return fmt.Errorf("failed to download file after %d attempts", f.config.MaxRetries)
}

func (f *FTP) removeRemoteFile(filePath string) error {
	f.Lock()
	defer f.Unlock()

	// get the remote file path from the local file path and the remote directory
	remotePath := strings.Replace(filePath, f.config.LocalDir, f.config.RemoteDir, 1)

	// Delete the file from the FTP server
	err := f.client.Delete(remotePath)
	if err != nil {
		return err
	}

	return nil
}

func (f *FTP) removeLocalFile(filePath string) error {
	f.Lock()
	defer f.Unlock()

	err := os.Remove(filePath)
	if err != nil {
		return err
	}

	return nil
}
func (f *FTP) AddDirectoriesToWatcher(watcher *fsnotify.Watcher, rootDir string) error {
	switch f.Direction {
	case LocalToRemote:
		return filepath.Walk(rootDir, func(path string, info os.FileInfo, err error) error {
			if info.IsDir() {
				err = watcher.Add(path)
				if err != nil {
					return err
				}
				logger.Println("Adding watcher to directory:", path)
			}
			return nil
		})
	case RemoteToLocal:
		var prevFiles map[string]os.FileInfo
		for {
			// Read the remote directory and its subdirectories.
			newFiles := make(map[string]os.FileInfo)
			err := f.walkRemoteDir(rootDir, newFiles)
			if err != nil {
				return err
			}
			// Check for new or removed files.
			if prevFiles != nil {
				for p, file := range newFiles {
					prevFile, exists := prevFiles[p]
					if !exists || prevFile.ModTime().Before(file.ModTime()) {
						f.Pool.WG.Add(1)
						f.Pool.Tasks <- worker.Task{EventType: fsnotify.Write, Name: p}

					}
				}
				for p := range prevFiles {
					_, exists := newFiles[p]
					if !exists {
						f.Pool.WG.Add(1)

						f.Pool.Tasks <- worker.Task{EventType: fsnotify.Remove, Name: p}
						logger.Println("File removed:", p)
					}
				}
			}
			prevFiles = newFiles

			// Add a condition to stop the infinite loop.
			// For instance, if context has been cancelled:
			select {
			case <-f.ctx.Done():
				return nil
			default:
				// Wait for a while before checking again.
				time.Sleep(time.Second * 1)
			}
		}
	}
	return nil
}

func (f *FTP) Stat(path string) (os.FileInfo, error) {
	f.Lock()
	defer f.Unlock()

	// Calculate the remote file path
	remotePath := filepath.Join(f.config.RemoteDir, filepath.Base(path))

	// Fetch the file info from the FTP server
	fileInfo, err := f.client.Stat(remotePath)
	if err != nil {
		return nil, err
	}

	return fileInfo, nil
}
func (f *FTP) walkRemoteDir(dir string, files map[string]os.FileInfo) error {
	// Use the ReadDir to list the contents of the directory.
	fileInfos, err := f.client.ReadDir(dir)
	if err != nil {
		return err
	}

	for _, fileInfo := range fileInfos {
		// Check if the fileInfo represents a file or a directory.
		if fileInfo.IsDir() {
			// If it's a directory, add it to the files map and recursively call walkRemoteDir.
			files[filepath.Join(dir, fileInfo.Name())] = fileInfo
			err = f.walkRemoteDir(filepath.Join(dir, fileInfo.Name()), files)
			if err != nil {
				return err
			}
		} else {
			// If it's a file, add it to the files map.
			files[filepath.Join(dir, fileInfo.Name())] = fileInfo
		}
	}

	return nil
}

func (f *FTP) checkOrCreateDir(dirPath string) error {
	pathParts := strings.Split(dirPath, "/")
	currentPath := ""

	switch f.Direction {
	case LocalToRemote:
		for _, part := range pathParts {
			currentPath = currentPath + "/" + part
			// First, try to make the directory
			_, err := f.client.Mkdir(currentPath)
			if err != nil {
				// If that fails, assume it's because the directory already exists and check it
				_, err := f.client.ReadDir(currentPath)
				if err != nil {
					// If that also fails, return the error
					return err
				}
			}
		}
	case RemoteToLocal:
		for _, part := range pathParts {
			currentPath = filepath.Join(currentPath, part)
			err := os.MkdirAll(currentPath, os.ModePerm)
			if err != nil {
				// If that fails, assume it's because the directory already exists
				if !os.IsExist(err) {
					// If the error is not because the directory already exists, return the error
					return err
				}
			}
		}
	}

	return nil
}

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
