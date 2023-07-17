package sftp

import (
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

type SyncDirection int

const (
	LocalToRemote SyncDirection = iota
	RemoteToLocal
)

type SFTP struct {
	sync.Mutex
	Client    *sftp.Client
	Direction SyncDirection
	config    *ExtraConfig
	Watcher   *fsnotify.Watcher
}

type ExtraConfig struct {
	Username   string
	Password   string
	LocalDir   string
	RemoteDir  string
	Retries    int
	MaxRetries int
}

func Connect(address string, port int, direction SyncDirection, config *ExtraConfig) (*SFTP, error) {
	var authMethod ssh.AuthMethod
	if config != nil {
		authMethod = ssh.Password(config.Password)
	} else {
		authMethod = ssh.Password("anonymous")
	}

	clientConfig := &ssh.ClientConfig{
		User:            config.Username,
		Auth:            []ssh.AuthMethod{authMethod},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	}

	conn, err := ssh.Dial("tcp", fmt.Sprintf("%s:%d", address, port), clientConfig)
	if err != nil {
		return nil, err
	}

	client, err := sftp.NewClient(conn)
	if err != nil {
		return nil, err
	}

	return &SFTP{
		Client:    client,
		Direction: direction,
		config:    config,
	}, nil
}

func (c *SFTP) WatchDirectory() {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Fatal(err)
	}
	defer func(watcher *fsnotify.Watcher) {
		err := watcher.Close()
		if err != nil {
			log.Println("Error closing watcher:", err)
		}
	}(watcher)

	go func() {
		for {
			select {
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				if event.Op&fsnotify.Write == fsnotify.Write {
					log.Println("Modified file:", event.Name)
					if c.Direction == LocalToRemote {
						err := c.uploadFile(event.Name)
						if err != nil {
							log.Println("Error uploading file:", err)
						}
					}
					if c.Direction == RemoteToLocal {
						err := c.downloadFile(event.Name)
						if err != nil {
							log.Println("Error downloading file:", err)
						}
					}
				}
			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				log.Println("Error:", err)
			}
		}
	}()

	err = watcher.Add(c.config.LocalDir)
	if err != nil {
		log.Fatal(err)
	}

	<-make(chan struct{})
}

func (c *SFTP) uploadFile(filePath string) error {
	c.Lock()
	defer c.Unlock()

	srcFile, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer func(srcFile *os.File) {
		err := srcFile.Close()
		if err != nil {
			log.Println("Error closing file:", err)
		}
	}(srcFile)

	dstFile, err := c.Client.Create(filepath.Join(c.config.RemoteDir, filepath.Base(filePath)))
	if err != nil {
		return err
	}
	defer func(dstFile *sftp.File) {
		err := dstFile.Close()
		if err != nil {
			log.Println("Error closing file:", err)
		}
	}(dstFile)

	_, err = io.Copy(dstFile, srcFile)
	return err
}

func (c *SFTP) downloadFile(remotePath string) error {
	c.Lock()
	defer c.Unlock()

	srcFile, err := c.Client.Open(remotePath)
	if err != nil {
		return err
	}
	defer func(srcFile *sftp.File) {
		err := srcFile.Close()
		if err != nil {
			log.Println("Error closing file:", err)
		}
	}(srcFile)

	dstFile, err := os.Create(filepath.Join(c.config.LocalDir, filepath.Base(remotePath)))
	if err != nil {
		return err
	}
	defer func(dstFile *os.File) {
		err := dstFile.Close()
		if err != nil {
			log.Println("Error closing file:", err)
		}
	}(dstFile)

	_, err = io.Copy(dstFile, srcFile)
	return err
}

func (c *SFTP) Mkdir(dir string) error {
	c.Lock()
	defer c.Unlock()

	err := c.Client.Mkdir(filepath.Join(c.config.RemoteDir, dir))
	return err
}