package ssh

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sync"

	"github.com/fsnotify/fsnotify"
	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

type Config struct {
	Host     string
	Port     string
	Username string
	Password string
}

type SSH struct {
	Config
	Client *sftp.Client
	mutex  sync.Mutex
	ctx    context.Context    // Context field
	cancel context.CancelFunc // Cancel function field
}

func NewSSH(config Config) (*SSH, error) {
	sshConfig := &ssh.ClientConfig{
		User:            config.Username,
		Auth:            []ssh.AuthMethod{ssh.Password(config.Password)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	}

	client, err := ssh.Dial("tcp", config.Host+":"+config.Port, sshConfig)
	if err != nil {
		return nil, fmt.Errorf("dial ssh: %w", err)
	}

	sftpClient, err := sftp.NewClient(client)
	if err != nil {
		return nil, fmt.Errorf("new sftp client: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background()) // Create a cancelable context

	return &SSH{
		Config: config,
		Client: sftpClient,
		ctx:    ctx,    // Initialize with created context
		cancel: cancel, // Initialize with created cancel function
	}, nil
}

func (ssh *SSH) WatchDirectory(localPath string, remotePath string) {
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
					go ssh.uploadFile(event.Name, remotePath)
				}
			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				log.Println("error:", err)
			case <-ssh.ctx.Done():
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
		log.Println("ERROR during file walk: ", err)
	}

	<-ssh.ctx.Done()
}

func (ssh *SSH) uploadFile(localPath string, remotePath string) {
	ssh.mutex.Lock()
	defer ssh.mutex.Unlock()

	localFile, err := os.Open(localPath)
	if err != nil {
		log.Println("ERROR opening file: ", err)
		return
	}
	defer localFile.Close()

	remoteFile, err := ssh.Client.Create(filepath.Join(remotePath, filepath.Base(localPath)))
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

func (ssh *SSH) Close() error {
	ssh.mutex.Lock()
	defer ssh.mutex.Unlock()

	ssh.cancel() // Cancel the context

	if err := ssh.Client.Close(); err != nil {
		log.Println("ERROR closing SSH connection: ", err)
		return err
	}

	return nil
}
