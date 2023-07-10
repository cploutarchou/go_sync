package ftp

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/secsy/goftp"
)

type FTP struct {
	Config
	Client *goftp.Client
	mutex  sync.Mutex
	ctx    context.Context    // Context field
	cancel context.CancelFunc // Cancel function field
}

type Config struct {
	Host     string
	Port     string
	Username string
	Password string
}

func New(config Config) (*FTP, error) {
	cfg := goftp.Config{
		User:               config.Username,
		Password:           config.Password,
		ConnectionsPerHost: 10,
		Timeout:            30 * time.Second,
		Logger:             os.Stderr, // Logs to standard error
	}

	client, err := goftp.DialConfig(cfg, config.Host+":"+config.Port)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithCancel(context.Background()) // Create a cancelable context

	return &FTP{
		Config: config,
		Client: client,
		mutex:  sync.Mutex{},
		ctx:    ctx,    // Initialize with created context
		cancel: cancel, // Initialize with created cancel function
	}, nil
}

func (ftp *FTP) WatchDirectory(localPath string, remotePath string) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Fatal(err)
	}
	defer watcher.Close()

	go func() {
		for {
			select {
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				log.Println("event:", event)
				if event.Op&fsnotify.Write == fsnotify.Write || event.Op&fsnotify.Create == fsnotify.Create {
					log.Println("modified file:", event.Name)
					go ftp.uploadFile(event.Name, remotePath)
				}
			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				log.Println("error:", err)
			case <-ftp.ctx.Done():
				return
			}
		}
	}()

	err = filepath.Walk(localPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if info.IsDir() {
			err = watcher.Add(path)
			if err != nil {
				log.Fatal(err)
			}
		}

		return nil
	})

	if err != nil {
		log.Println("ERROR", err)
	}

	<-ftp.ctx.Done()
}

func (ftp *FTP) uploadFile(localPath string, remotePath string) {
	ftp.mutex.Lock()
	defer ftp.mutex.Unlock()

	// check if remote directory exists and is empty
	files, err := ftp.Client.ReadDir(remotePath)
	if err != nil {
		log.Printf("Error reading remote directory: %v", err)
		return
	}
	if len(files) == 0 {
		log.Printf("Warning: remote directory %s is empty", remotePath)
	}

	file, err := os.Open(localPath)
	if err != nil {
		log.Printf("Error opening file: %v", err)
		return
	}

	remoteFilePath := filepath.Join(remotePath, filepath.Base(localPath))
	log.Printf("Uploading %s to %s", localPath, remoteFilePath)
	if err := ftp.Client.Store(remoteFilePath, file); err != nil {
		log.Printf("Upload failed: %v", err)
	}

	defer file.Close()
}

func (ftp *FTP) Close() error {
	ftp.mutex.Lock()
	defer ftp.mutex.Unlock()

	ftp.cancel() // Cancel the context, which will stop the WatchDirectory goroutine

	err := ftp.Client.Close()
	if err != nil {
		log.Printf("Error closing FTP connection: %v", err)
		return err
	}
	return nil
}
