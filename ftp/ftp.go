package ftp

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"net/textproto"
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
	conn      *textproto.Conn
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
	address = net.JoinHostPort(address, fmt.Sprintf("%d", port))
	conn, err := net.Dial("tcp", address)
	if err != nil {
		return nil, err
	}

	ftp := &FTP{
		conn:      textproto.NewConn(conn),
		Direction: direction,
		ctx:       context.Background(),
		Pool:      worker.NewWorkerPool(10),
	}
	ftp.config = config

	if config != nil {
		err = ftp.Login(config.Username, config.Password)
	} else {
		err = ftp.Login("anonymous", "anonymous")
	}
	if err != nil {
		_ = ftp.conn.Close()
		return nil, err
	}
	logger.Println("Connected to FTP server.")
	return ftp, nil
}

func (f *FTP) Login(username, password string) error {

	_, err := f.conn.Cmd("USER %s", username)
	if err != nil {
		return err
	}

	_, err = f.conn.Cmd("PASS %s", password)
	if err != nil {
		return err
	}

	return nil
}

func (f *FTP) List() ([]string, error) {
	f.Lock()
	defer f.Unlock()

	_, err := f.conn.Cmd("PASV")
	if err != nil {
		return nil, err
	}

	_, err = f.conn.Cmd("LIST")
	if err != nil {
		return nil, err
	}

	reader := bufio.NewReader(f.conn.R)
	line, _, err := reader.ReadLine()
	var files []string
	for err == nil {
		files = append(files, string(line))
		line, _, err = reader.ReadLine()
	}

	return files, nil
}
func (f *FTP) initialSync() error {
	return f.syncDir(f.config.LocalDir, f.config.RemoteDir)
}

func (f *FTP) syncDir(localDir, remoteDir string) error {
	switch f.Direction {
	case LocalToRemote:
		localFiles, err := os.ReadDir(localDir)
		if err != nil {
			return err
		}
		for _, file := range localFiles {
			localFilePath := filepath.Join(localDir, file.Name())
			remoteFilePath := filepath.Join(remoteDir, file.Name())

			logger.Println("localFilePath", localFilePath)
			logger.Println("remoteFilePath", remoteFilePath)

			if file.IsDir() {
				logger.Println("file is dir")
				logger.Println("remoteFilePath", remoteFilePath)
				err = f.checkOrCreateDir(remoteFilePath)
				if err != nil {
					return err
				}
				err = f.syncDir(localFilePath, remoteFilePath)
				if err != nil {
					return err
				}
			} else {
				logger.Println("file is not dir")
				logger.Println("remoteFilePath", remoteFilePath)
				//stat remote file and if it doesn't exist upload it to the server
				_, err := f.Stat(remoteFilePath)
				if err != nil {
					err = f.uploadFile(localFilePath)
					if err != nil {
						return err
					}
				}
			}
		}
	case RemoteToLocal:
		// Read the remote directory and all subdirectories.
		remoteFiles, err := f.conn.Cmd("NLST %f", remoteDir)
		fmt.Println("remoteFiles", remoteFiles)
		if err != nil {
			return err
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
		err = watcher.Close()
		if err != nil {
			logger.Println("Error closing watcher:", err)
		}
	}(watcher)

	events := make(chan fsnotify.Event)

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
				events <- event
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
	f.Lock()
	defer f.Unlock()

	file, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer func(file *os.File) {
		err := file.Close()
		if err != nil {
			logger.Println("Error closing file:", err)
		}
	}(file)

	for i := 0; i < f.config.MaxRetries; i++ {
		_, err = f.conn.Cmd("PASV")
		if err != nil {
			if i == f.config.MaxRetries-1 {
				return err
			}
			continue
		}

		_, err = f.conn.Cmd("STOR %s", filepath.Join(f.config.RemoteDir, filepath.Base(filePath)))
		if err != nil {
			if i == f.config.MaxRetries-1 {
				return err
			}
			continue
		}

		_, err = io.Copy(f.conn.W, file)
		if err != nil {
			if i == f.config.MaxRetries-1 {
				return err
			}
			continue
		}

		break
	}

	return nil
}

func (f *FTP) downloadFile(name string) error {
	f.Lock()
	defer f.Unlock()

	file, err := os.Create(filepath.Join(f.config.LocalDir, name))
	if err != nil {
		return err
	}
	defer func(file *os.File) {
		err := file.Close()
		if err != nil {
			logger.Println("Error closing file:", err)
		}
	}(file)

	for i := 0; i < f.config.MaxRetries; i++ {
		_, err = f.conn.Cmd("PASV")
		if err != nil {
			if i == f.config.MaxRetries-1 {
				return err
			}
			continue
		}

		_, err = f.conn.Cmd("RETR %s", filepath.Join(f.config.RemoteDir, name))
		if err != nil {
			if i == f.config.MaxRetries-1 {
				return err
			}
			continue
		}

		_, err = io.Copy(file, f.conn.R)
		if err != nil {
			if i == f.config.MaxRetries-1 {
				return err
			}
			continue
		}

		break
	}

	return nil
}

func (f *FTP) removeRemoteFile(filePath string) error {
	f.Lock()
	defer f.Unlock()

	_, err := f.conn.Cmd("DELE %s", filepath.Join(f.config.RemoteDir, filepath.Base(filePath)))
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

						f.Pool.Tasks <- worker.Task{EventType: fsnotify.Create, Name: p}
						logger.Println("New or modified file:", p)
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
			// Wait for a while before checking again.
			time.Sleep(time.Second * 1)
		}
	}
	return nil
}

func (f *FTP) Stat(path string) (os.FileInfo, error) {
	f.Lock()
	defer f.Unlock()

	_, err := f.conn.Cmd("PASV")
	if err != nil {
		return nil, err
	}

	_, err = f.conn.Cmd("STAT %s", path)
	if err != nil {
		return nil, err
	}

	reader := bufio.NewReader(f.conn.R)
	line, _, err := reader.ReadLine()
	if err != nil {
		return nil, err
	}

	return os.Stat(string(line))
}

// walkRemoteDir traverses a remote directory and its subdirectories,
// adding all files it finds to the provided map.
func (f *FTP) walkRemoteDir(dir string, files map[string]os.FileInfo) error {
	_, err := f.conn.Cmd("PASV")
	if err != nil {
		return err
	}

	// Use the NLST command to list the contents of the directory.
	_, err = f.conn.Cmd("NLST %s", dir)
	if err != nil {
		return err
	}

	// Read the response from the FTP server.
	reader := bufio.NewReader(f.conn.R)
	line, _, err := reader.ReadLine()
	for err == nil {
		// Check if the line represents a file or a directory.
		fileInfo, err := f.Stat(filepath.Join(dir, string(line)))
		if err == nil {
			// If it's a directory, add it to the files map and recursively call walkRemoteDir.
			if fileInfo.IsDir() {
				files[filepath.Join(dir, string(line))] = fileInfo
				err = f.walkRemoteDir(filepath.Join(dir, string(line)), files)
				if err != nil {
					return err
				}
			} else {
				// If it's a file, add it to the files map.
				files[filepath.Join(dir, string(line))] = fileInfo
			}
		} else {
			// If there was an error getting the file info, skip the entry.
			logger.Println("Error getting file info:", err)
		}

		line, _, err = reader.ReadLine()
	}

	// Ignore the error for "EOF" as it's expected when there are no more lines to read.
	if err != io.EOF {
		return err
	}

	return nil
}
func (f *FTP) checkOrCreateDir(dirPath string) error {
	switch f.Direction {
	case LocalToRemote:
		id, err := f.conn.Cmd("CWD %s", dirPath)
		if err == nil {
			logger.Println("Directory exists:", id)
			_, _, err = f.conn.ReadCodeLine(250)
			switch {
			case strings.Contains(err.Error(), "250"):
				return nil
			default:
				// Directory doesn't exist, create it.
				_, err = f.conn.Cmd("MKD %s", dirPath)
				if err != nil {
					return err
				}
				// give the server time to create the directory
				time.Sleep(time.Second * 1)
				// Check if the directory was created. and give the proper permissions
				_, err = f.conn.Cmd("CWD %s", dirPath)
				if err != nil {
					return err
				}
				_, _, err = f.conn.ReadCodeLine(250)
				if err != nil {
					return err
				}
				//give the proper permissions
				_, err = f.conn.Cmd("SITE CHMOD 777 %s", dirPath)
				if err != nil {
					return err
				}
				return nil
			}
		}

	case RemoteToLocal:
		// Read the remote directory and all subdirectories.
		_, err := f.conn.Cmd("NLST %f", dirPath)
		if err != nil {
			return err

		}
		// read the response from the FTP server.
		reader := bufio.NewReader(f.conn.R)
		line, _, err := reader.ReadLine()
		for err == nil {
			// Check if the line represents a file or a directory.
			fileInfo, err := f.Stat(filepath.Join(dirPath, string(line)))
			if err == nil {
				// If it's a directory, add it to the files map and recursively call walkRemoteDir.
				if fileInfo.IsDir() {
					err = f.checkOrCreateDir(filepath.Join(dirPath, string(line)))
					if err != nil {
						return err
					}
				} else {
					// If it's a file, add it to the files map.
					return nil
				}
			} else {
				// If there was an error getting the file info, skip the entry.
				logger.Println("Error getting file info:", err)
			}

			line, _, err = reader.ReadLine()
		}

	}

	return nil
}
