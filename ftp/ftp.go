package ftp

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/secsy/goftp"

	"github.com/cploutarchou/syncpkg/worker"
	"github.com/fsnotify/fsnotify"
)

var logger = log.New(os.Stdout, "ftp: ", log.Lshortfile)

// SyncDirection is the direction of the sync (LocalToRemote or RemoteToLocal)
type SyncDirection int

const (
	//LocalToRemote is the direction of the sync from local to remote pc/server
	LocalToRemote SyncDirection = iota
	//RemoteToLocal is the direction of the sync from remote to local pc/server
	RemoteToLocal
)

// FTP is the struct that holds the ftp client and the sync direction
type FTP struct {
	sync.Mutex
	//client is the ftp client that is used to connect to the ftp server
	client *goftp.Client
	//Direction is the direction of the sync (LocalToRemote or RemoteToLocal)
	Direction SyncDirection
	//config is the struct that holds the extra config for the ftp connection
	config *ExtraConfig
	//Watcher is the fsnotify watcher that is used to watch the local directory
	Watcher *fsnotify.Watcher
	//Pool is the worker pool that is used to process the fsnotify events
	Pool *worker.Pool
	//ctx is the context that is used to cancel the watcher
	ctx context.Context
}

// ExtraConfig is the struct that holds the extra config for the ftp connection
type ExtraConfig struct {
	//Username is the username that is used to connect to the ftp server
	Username string
	//Password is the password that is used to connect to the ftp server
	Password string
	//LocalDir is the local directory that is used to sync with the remote directory
	LocalDir string
	//RemoteDir is the remote directory that is used to sync with the local directory
	RemoteDir string
	//Retries is the number of retries that the ftp client will try to upload/download a file
	Retries int
	//MaxRetries is the number of retries that the ftp client will try to upload/download a file
	MaxRetries int
}

// Connect is a function used to establish a connection to an FTP server and return an FTP client for file synchronization.
//
// - address is the address of the FTP server.
//
// - port is the port of the FTP server.
//
// - direction is the direction of the synchronization, which can be either LocalToRemote or RemoteToLocal.
//
//   - config is a pointer to the ExtraConfig struct that holds additional configuration settings for the FTP connection,
//     including FTP server credentials (username and password), local and remote directories, and synchronization retries.
//
// Example:
//
//	ftp, err := ftp.Connect("localhost", 21, ftp.LocalToRemote, &ftp.ExtraConfig{
//	    Username:   "username",
//	    Password:   "password",
//	    LocalDir:   "localDir",
//	    RemoteDir:  "remoteDir",
//	    Retries:    3,
//	    MaxRetries: 3,
//	})
//
//	if err != nil {
//	    log.Fatal(err)
//	}
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

// initialSync is a method of the FTP struct that performs the initial synchronization between the local directory
// and the remote directory. It calls the syncDir method to handle the synchronization process.
//
// This method is used internally to synchronize the directories when the FTP connection is initially established.
// The synchronization direction is determined by the value of f.Direction, which can be either LocalToRemote or RemoteToLocal.
//
// - Returns an error if any error occurs during the synchronization process.
func (f *FTP) initialSync() error {
	return f.syncDir(f.config.LocalDir, f.config.RemoteDir)
}

// syncDir is a method of the FTP struct that synchronizes files between the local directory and the remote directory.
// The synchronization direction depends on the value of f.Direction, which can be either LocalToRemote or RemoteToLocal.
//
// - localDir is the path to the local directory to be synchronized.
//
// - remoteDir is the path to the remote directory to be synchronized with.
//
// If f.Direction is LocalToRemote, this method will perform the following actions:
// - Recursively traverse the local directory and its subdirectories.
// - Check if each file exists on the remote server. If not, it will upload the file to the server.
// - If the file is a directory, it will create the corresponding directory on the remote server if it doesn't exist.
//
// If f.Direction is RemoteToLocal, this method will perform the following actions:
// - Recursively traverse the remote directory and its subdirectories.
// - Check if each file exists in the local file system. If not, it will download the file from the server.
// - If the file is a directory, it will create the corresponding directory in the local file system if it doesn't exist.
//
// This method is used internally by the synchronization process and is not intended to be called directly.
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

// WatchDirectory is a method of the FTP struct that sets up a file system watcher to monitor changes in the local directory.
// It starts a worker pool and performs an initial synchronization between the local directory and the remote directory
// based on the specified synchronization direction (LocalToRemote or RemoteToLocal).
//
// The method uses fsnotify package to monitor file system events such as file creations, modifications, and deletions.
// When a file system event is detected, it creates a worker task and adds it to the worker pool for processing.
// The worker tasks are handled by the Worker method, which performs the necessary file transfers to keep the directories in sync.
//
// The synchronization is bidirectional, meaning that changes made in the local directory will be propagated to the remote directory,
// and changes made in the remote directory will be reflected in the local directory.
//
//   - Please note that this method enters an infinite loop to continuously monitor file system events until the context is canceled.
//     The method will block until the context is done or an error occurs during the synchronization process.
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

// uploadFile is a method of the FTP struct that uploads a file to the remote FTP server.
//
// - filePath is the path to the local file that needs to be uploaded.
//
// The method attempts to upload the file to the FTP server for a maximum number of retries specified in f.config.MaxRetries.
// If the upload fails for any reason, the method will log the error and retry until the maximum number of retries is reached.
//
// The method calculates the remote file path based on the local file path and the remote directory specified in f.config.RemoteDir.
// It then opens the local file for reading and uploads it to the FTP server using the f.client.Store method.
//
// - Returns an error if the file upload fails after the maximum number of retries.
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

// downloadFile is a method of the FTP struct that downloads a file from the remote FTP server to the local file system.
//
// - name is the name of the file to be downloaded from the remote server.
//
// The method attempts to download the file from the FTP server for a maximum number of retries specified in f.config.MaxRetries.
// If the download fails for any reason, the method will log the error and retry until the maximum number of retries is reached.
//
// The method calculates the remote file path based on the file name and the remote directory specified in f.config.RemoteDir.
// It then creates a new local file and downloads the remote file from the FTP server using the f.client.Retrieve method.
//
// - Returns an error if the file download fails after the maximum number of retries.
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

// removeRemoteFile is a method of the FTP struct that deletes a file from the remote FTP server.
//
// - filePath is the path to the local file whose remote counterpart needs to be deleted.
//
// The method calculates the remote file path based on the local file path and the remote directory specified in f.config.RemoteDir.
// It then sends a delete command to the FTP server using the f.client.Delete method to remove the file from the server.
//
// - Returns an error if the file deletion operation fails.
func (f *FTP) removeRemoteFile(filePath string) error {
	f.Lock()
	defer f.Unlock()

	// Get the remote file path from the local file path and the remote directory
	remotePath := strings.Replace(filePath, f.config.LocalDir, f.config.RemoteDir, 1)

	// Delete the file from the FTP server
	err := f.client.Delete(remotePath)
	if err != nil {
		return err
	}

	return nil
}

// removeLocalFile is a method of the FTP struct that deletes a file from the local file system.
//
// - filePath is the path to the local file that needs to be deleted.
//
// The method uses the os.Remove function to delete the specified file from the local file system.
//
// - Returns an error if the file deletion operation fails.
func (f *FTP) removeLocalFile(filePath string) error {
	f.Lock()
	defer f.Unlock()

	err := os.Remove(filePath)
	if err != nil {
		return err
	}

	return nil
}

// AddDirectoriesToWatcher is a method of the FTP struct that adds directories and their subdirectories to the fsnotify watcher.
//
// - watcher is a pointer to the fsnotify.Watcher that will be used to watch for file system events.
//
// - rootDir is the root directory from which directories and subdirectories will be added to the watcher.
//
// The method behaves differently based on the sync direction specified in f.Direction:
//
//   - LocalToRemote: It walks the local directory tree starting from rootDir and adds all directories to the fsnotify watcher.
//     Each time a new directory is added, the method logs the event and starts watching for file system events in that directory.
//
//   - RemoteToLocal: It continuously reads the remote directory tree and its subdirectories and compares it with the previous state.
//     When new files are detected or files are modified on the remote server, the method enqueues tasks to the worker pool for processing.
//     If files are removed from the remote server, the method enqueues tasks to the worker pool to handle the file removal.
//     The method keeps monitoring for changes in the remote directory tree until the context (f.ctx) is canceled or an error occurs.
//
// - Returns an error if there is a problem while adding directories to the fsnotify watcher or monitoring the remote directory tree.
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

			// TODO : Add a condition to stop the infinite loop.
			// For instance, if the context (f.ctx) has been canceled:
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

// Stat is a method of the FTP struct that retrieves file information (os.FileInfo) for a remote file on the FTP server.
//
// - path is the path of the remote file for which file information is required.
//
// The method calculates the remote file path by joining the remote directory (f.config.RemoteDir) with the base name of the specified path.
// It then fetches the file information from the FTP server using the f.client.Stat method.
//
// - Returns the file information (os.FileInfo) for the remote file if the operation is successful.
//
// - Returns an error if there is a problem retrieving the file information from the FTP server.
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

// walkRemoteDir is a method of the FTP struct that recursively lists the contents of a remote directory on the FTP server and populates the provided map with file information (os.FileInfo) for each file found.
//
// - dir is the path of the remote directory to be traversed.
//
// - files is the map that will be populated with file information for each file found in the remote directory and its subdirectories.
//
// The method uses f.client.ReadDir to list the contents of the specified remote directory. For each item in the directory, it checks if it represents a file or a subdirectory. If it's a subdirectory, it adds it to the files map and recursively calls itself with the subdirectory path. If it's a file, it adds it to the files map with its path.
//
// - Returns an error if there is a problem reading the remote directory or its subdirectories.
//
// Note: The provided map (files) should be initialized before calling this method to collect the file information. The method only collects file information and does not modify the map if it already contains data.
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

// checkOrCreateDir is a method of the FTP struct that checks if the specified directory exists on either the local or remote side (depending on the sync direction) and creates it if it doesn't exist.
//
// - dirPath is the path of the directory to be checked and created (if necessary).
//
// The method first splits the directory path into individual parts using strings.Split. Then, depending on the sync direction (LocalToRemote or RemoteToLocal), it either checks and creates the directory on the remote FTP server using f.client.Mkdir or on the local machine using os.MkdirAll.
//
// - For LocalToRemote sync direction, the method uses f.client.Mkdir to try creating the directory on the FTP server. If the directory already exists on the server, it assumes the operation is successful. If the directory does not exist, it returns an error.
//
// - For RemoteToLocal sync direction, the method uses os.MkdirAll to create the directory on the local machine. If the directory already exists locally, it assumes the operation is successful. If the directory does not exist, it creates all necessary parent directories recursively.
//
// - Returns an error if there is a problem creating the directory on either the local or remote side.
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

// Worker starts a new worker goroutine that processes tasks received from the worker pool.
//
// The method listens for tasks on the f.Pool.Tasks channel, which is a buffered channel used for queuing tasks. Each task contains an EventType (fsnotify.Write, fsnotify.Remove, fsnotify.Rename, fsnotify.Chmod) and a Name (the file path of the task).
//
// Depending on the EventType and the sync direction (LocalToRemote or RemoteToLocal), the method performs different actions:
//
// - For fsnotify.Write events:
//   - LocalToRemote: Calls f.uploadFile to upload the modified or newly created file to the remote FTP server.
//   - RemoteToLocal: Calls f.downloadFile to download the modified or newly created file from the remote FTP server to the local machine.
//
// - For fsnotify.Remove events:
//   - LocalToRemote: Calls f.removeRemoteFile to delete the specified file from the remote FTP server.
//   - RemoteToLocal: Calls f.removeLocalFile to delete the specified file from the local machine.
//
// - For fsnotify.Rename events:
//   - LocalToRemote: Calls f.uploadFile to upload the renamed file to the remote FTP server, then calls f.removeRemoteFile to delete the original file from the server.
//   - RemoteToLocal: Calls f.downloadFile to download the renamed file from the remote FTP server to the local machine, then calls f.removeLocalFile to delete the original file from the local machine.
//
// - For fsnotify.Chmod events: The method logs a message indicating that the permissions of a file have changed.
//
// After processing each task, the method marks it as done using f.Pool.WG.Done(), which decrements the worker pool's WaitGroup counter.
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
