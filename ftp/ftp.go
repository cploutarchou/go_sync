package ftp

import (
	"github.com/fsnotify/fsnotify"
	"github.com/secsy/goftp"
	"log"
	"os"
	"path/filepath"
	"time"
)

type FTP struct {
	Config
	Client *goftp.Client
}

type Config struct {
	Host     string
	Port     string
	Username string
	Password string
	options  map[string]string
}

func New(config Config) *FTP {
	cfg := goftp.Config{
		User:               config.Username,
		Password:           config.Password,
		ConnectionsPerHost: 10,
		Timeout:            60 * time.Second,
		Logger:             os.Stderr, // Logs to standard error
	}

	client, err := goftp.DialConfig(cfg, config.Host+":"+config.Port)

	if err != nil {
		log.Fatalf("Failed to connect to FTP server: %v", err)
		return nil
	}

	return &FTP{
		Config: config,
		Client: client,
	}
}

func (ftp *FTP) WatchDirectory(localPath string, remotePath string) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Fatal(err)
	}
	defer watcher.Close()

	done := make(chan bool)
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
					ftp.uploadFile(event.Name, remotePath)
				}
			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				log.Println("error:", err)
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

	<-done
}

func (ftp *FTP) uploadFile(localPath string, remotePath string) {
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
