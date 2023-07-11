package ftp

import (
	"context"
	"errors"
	"fmt"
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
		return nil, fmt.Errorf("dial config: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background()) // Create a cancelable context

	return &FTP{
		Config: config,
		Client: client,
		ctx:    ctx,    // Initialize with created context
		cancel: cancel, // Initialize with created cancel function
	}, nil
}

func (ftp *FTP) WatchDirectory(localPath string, remotePath string) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Fatal(fmt.Errorf("create new watcher: %w", err))
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
				log.Fatal(fmt.Errorf("add path to watcher: %w", err))
			}
		}

		return nil
	})

	if err != nil {
		logError("filepath walk", err)
	}

	<-ftp.ctx.Done()
}

func (ftp *FTP) uploadFile(localPath string, remotePath string) {
	ftp.mutex.Lock()
	defer ftp.mutex.Unlock()

	// check if remote directory exists and is empty
	files, err := ftp.Client.ReadDir(remotePath)
	if err != nil {
		logError("read remote directory", err)
		return
	}
	if len(files) == 0 {
		log.Printf("Warning: remote directory %s is empty", remotePath)
	}

	file, err := os.Open(localPath)
	if err != nil {
		logError("open file", err)
		return
	}
	defer file.Close()

	remoteFilePath := filepath.Join(remotePath, filepath.Base(localPath))
	log.Printf("Uploading %s to %s", localPath, remoteFilePath)
	if err := ftp.Client.Store(remoteFilePath, file); err != nil {
		logError("store file", err)
	}
}

func (ftp *FTP) Close() error {
	ftp.mutex.Lock()
	defer ftp.mutex.Unlock()

	ftp.cancel() // Cancel the context, which will stop the WatchDirectory goroutine

	err := ftp.Client.Close()
	if err != nil {
		logError("close FTP connection", err)
	}
	return err
}

func logError(action string, err error) {
	if errors.Is(err, fsnotify.ErrEventOverflow) {
		log.Printf("%s: too many changes at once: %v", action, err)
	} else {
		log.Printf("%s: %v", action, err)
	}
}

