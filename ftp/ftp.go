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
	"sync"

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
	return ftp, nil
}

func (f *FTP) Login(username, password string) error {
	f.Lock()
	defer f.Unlock()

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
	f.Lock()
	defer f.Unlock()

	switch f.Direction {
	case LocalToRemote:
		localFiles, err := os.ReadDir(f.config.LocalDir)
		if err != nil {
			return err
		}

		for _, file := range localFiles {
			err = f.uploadFile(filepath.Join(f.config.LocalDir, file.Name()))
			if err != nil {
				return err
			}
		}

	case RemoteToLocal:
		remoteFiles, err := f.List()
		if err != nil {
			return err
		}

		for _, file := range remoteFiles {
			err = f.downloadFile(file)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

func (f *FTP) WatchDirectory() {
	// Starting the worker pool
	for i := 0; i < cap(f.Pool.Tasks); i++ {
		go f.worker()
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
	go f.watcherWorker(1, events)

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
