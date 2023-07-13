package sftp

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

type SyncDirection int

const (
	LocalToRemote SyncDirection = iota
	RemoteToLocal
)

type Config struct {
	Host         string
	Port         string
	Username     string
	Password     string
	SyncDir      SyncDirection
	LocalPath    string
	RemotePath   string
	SyncInterval time.Duration
}

type SFTP struct {
	Config
	Client *sftp.Client
	mutex  sync.Mutex
	ctx    context.Context
	cancel context.CancelFunc
}

func NewSFTP(config Config) (*SFTP, error) {
	sshConfig := &ssh.ClientConfig{
		User:            config.Username,
		Auth:            []ssh.AuthMethod{ssh.Password(config.Password)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         30 * time.Second,
	}

	client, err := ssh.Dial("tcp", config.Host+":"+config.Port, sshConfig)
	if err != nil {
		return nil, fmt.Errorf("dial ssh: %w", err)
	}

	sftpClient, err := sftp.NewClient(client)
	if err != nil {
		return nil, fmt.Errorf("new sftp client: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	return &SFTP{
		Config: config,
		Client: sftpClient,
		ctx:    ctx,
		cancel: cancel,
	}, nil
}

func (sftp *SFTP) StartSync() {
	if sftp.SyncDir == LocalToRemote {
		go sftp.SyncLocalToRemote()
	} else {
		go sftp.SyncRemoteToLocal(sftp.SyncInterval)
	}
}

func (sftp *SFTP) SyncLocalToRemote() {
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
				if event.Op&fsnotify.Write == fsnotify.Write || event.Op&fsnotify.Create == fsnotify.Create {
					go sftp.uploadFile(event.Name, sftp.RemotePath)
				}
			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				log.Println("error:", err)
			case <-sftp.ctx.Done():
				return
			}
		}
	}()

	err = filepath.Walk(sftp.LocalPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			err = watcher.Add(path)
			if err != nil {
				return err
			}
		}
		return nil
	})

	if err != nil {
		log.Fatal(err)
	}

	<-sftp.ctx.Done()
}

func (sftp *SFTP) SyncRemoteToLocal(syncInterval time.Duration) {
	go func() {
		for {
			select {
			case <-time.After(syncInterval):
				err := sftp.downloadUpdatedFiles(sftp.LocalPath, sftp.RemotePath)
				if err != nil {
					log.Println("sync remote to local: ", err)
				}
			case <-sftp.ctx.Done():
				return
			}
		}
	}()
}

func (sftp *SFTP) downloadUpdatedFiles(localPath string, remotePath string) error {
	sftp.mutex.Lock()
	defer sftp.mutex.Unlock()

	files, err := sftp.Client.ReadDir(remotePath)
	if err != nil {
		return fmt.Errorf("read remote directory: %w", err)
	}

	for _, file := range files {
		remoteFilePath := filepath.Join(remotePath, file.Name())
		localFilePath := filepath.Join(localPath, file.Name())

		remoteFile, err := sftp.Client.Open(remoteFilePath)
		if err != nil {
			log.Println("ERROR opening remote file: ", err)
			continue
		}

		localFile, err := os.Create(localFilePath)
		if err != nil {
			log.Println("ERROR creating local file: ", err)
			continue
		}

		_, err = io.Copy(localFile, remoteFile)
		if err != nil {
			log.Println("ERROR copying file contents: ", err)
		}

		remoteFile.Close()
		localFile.Close()

		log.Printf("Downloaded %s to %s", remoteFilePath, localFilePath)
	}

	return nil
}

func (sftp *SFTP) uploadFile(localPath string, remotePath string) {
	sftp.mutex.Lock()
	defer sftp.mutex.Unlock()

	localFile, err := os.Open(localPath)
	if err != nil {
		log.Println("ERROR opening file: ", err)
		return
	}
	defer localFile.Close()

	remoteFile, err := sftp.Client.Create(filepath.Join(remotePath, filepath.Base(localPath)))
	if err != nil {
		log.Println("ERROR creating remote file: ", err)
		return
	}
	defer remoteFile.Close()

	_, err = io.Copy(remoteFile, localFile)
	if err != nil {
		log.Println("ERROR writing to remote file: ", err)
		return
	}

	log.Printf("Uploaded %s to %s", localPath, remotePath)
}

func (sftp *SFTP) Close() error {
	sftp.mutex.Lock()
	defer sftp.mutex.Unlock()

	sftp.cancel()

	if err := sftp.Client.Close(); err != nil {
		log.Println("ERROR closing SFTP connection: ", err)
		return err
	}

	return nil
}
