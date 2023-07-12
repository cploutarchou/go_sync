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

type SyncDirection int

const (
	LocalToRemote SyncDirection = iota
	RemoteToLocal
)

type FTP struct {
	Config
	Client *goftp.Client
	mutex  sync.Mutex
	ctx    context.Context
	cancel context.CancelFunc
}

type Config struct {
	Host       string
	Port       string
	Username   string
	Password   string
	SyncDir    SyncDirection // Added SyncDir field
	LocalPath  string        // Added LocalPath field
	RemotePath string        // Added RemotePath field
}

func New(config Config) (*FTP, error) {
	cfg := goftp.Config{
		User:               config.Username,
		Password:           config.Password,
		ConnectionsPerHost: 10,
		Timeout:            30 * time.Second,
		Logger:             os.Stderr,
	}

	client, err := goftp.DialConfig(cfg, config.Host+":"+config.Port)
	if err != nil {
		return nil, fmt.Errorf("dial config: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	return &FTP{
		Config: config,
		Client: client,
		ctx:    ctx,
		cancel: cancel,
	}, nil
}

func (ftp *FTP) StartSync() {
	switch ftp.SyncDir {
	case LocalToRemote:
		ftp.WatchDirectory(ftp.LocalPath, ftp.RemotePath)
	case RemoteToLocal:
		ftp.SyncRemoteToLocal(ftp.LocalPath, ftp.RemotePath)
	default:
		log.Printf("Invalid sync direction: %v", ftp.SyncDir)
	}
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
				if event.Op&fsnotify.Write == fsnotify.Write || event.Op&fsnotify.Create == fsnotify.Create {
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
	if err := ftp.Client.Store(remoteFilePath, file); err != nil {
		logError("store file", err)
	}
}

func (ftp *FTP) SyncRemoteToLocal(localPath string, remotePath string) {
	go func() {
		for {
			select {
			case <-time.After(1 * time.Minute):
				err := ftp.downloadUpdatedFiles(localPath, remotePath)
				if err != nil {
					logError("sync remote to local", err)
				}
			case <-ftp.ctx.Done():
				return
			}
		}
	}()
}

func (ftp *FTP) downloadUpdatedFiles(localPath string, remotePath string) error {
	ftp.mutex.Lock()
	defer ftp.mutex.Unlock()

	remoteFiles, err := ftp.Client.ReadDir(remotePath)
	if err != nil {
		return fmt.Errorf("read remote directory: %w", err)
	}

	for _, file := range remoteFiles {
		remoteFilePath := filepath.Join(remotePath, file.Name())
		localFilePath := filepath.Join(localPath, file.Name())

		localFileStat, err := os.Stat(localFilePath)
		if os.IsNotExist(err) || localFileStat.ModTime().Before(file.ModTime()) {
			ftp.downloadFile(localFilePath, remoteFilePath)
		}
	}

	return nil
}

func (ftp *FTP) downloadFile(localPath string, remotePath string) {
	file, err := os.Create(localPath)
	if err != nil {
		logError("create local file", err)
		return
	}
	defer file.Close()

	if err := ftp.Client.Retrieve(remotePath, file); err != nil {
		logError("retrieve file", err)
	}
}

func (ftp *FTP) Close() error {
	ftp.mutex.Lock()
	defer ftp.mutex.Unlock()

	ftp.cancel()

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
