package sftp

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/user"
	"path/filepath"
	"sync"

	"github.com/cploutarchou/syncpkg/worker"
	"github.com/fsnotify/fsnotify"
	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

type SyncDirection int

const (
	LocalToRemote SyncDirection = iota
	RemoteToLocal
)

var logger = log.New(os.Stdout, "sftp: ", log.Lshortfile)

type SFTP struct {
	Direction SyncDirection
	config    *ExtraConfig
	Watcher   *fsnotify.Watcher
	ctx       context.Context
	mu        sync.Mutex
	Client    *sftp.Client
	Pool      *worker.Pool
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
		ctx:       context.Background(),
		Pool:      worker.NewWorkerPool(10),
	}, nil
}

func ConnectSSHPair(address string, port int, direction SyncDirection, config *ExtraConfig) (*SFTP, error) {
	usr, err := user.Current()
	if err != nil {
		return nil, fmt.Errorf("cannot get user home directory: %w", err)
	}

	key, err := os.ReadFile(filepath.Join(usr.HomeDir, ".ssh", "id_rsa"))
	if err != nil {
		return nil, fmt.Errorf("unable to read private key: %w", err)
	}

	signer, err := ssh.ParsePrivateKey(key)
	if err != nil {
		return nil, fmt.Errorf("unable to parse private key: %w", err)
	}

	authMethod := ssh.PublicKeys(signer)

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
		ctx:       context.Background(),
		Pool:      worker.NewWorkerPool(10),
	}, nil
}

func (s *SFTP) initialSync() error {
	switch s.Direction {
	case LocalToRemote:
		localFiles, err := os.ReadDir(s.config.LocalDir)
		if err != nil {
			return err
		}
		for _, file := range localFiles {
			if file.IsDir() {
				err = s.checkOrCreateDir(filepath.Join(s.config.LocalDir, file.Name()))
				if err != nil {
					return err
				}
			} else {
				remoteFilePath := filepath.Join(s.config.RemoteDir, file.Name())
				_, err := s.Client.Stat(remoteFilePath)
				if err != nil {
					err = s.uploadFile(filepath.Join(s.config.LocalDir, file.Name()))
					if err != nil {
						return err
					}
				}
			}
		}

	case RemoteToLocal:
		remoteFiles, err := s.Client.ReadDir(s.config.RemoteDir)
		if err != nil {
			return err
		}

		for _, file := range remoteFiles {
			if file.IsDir() {
				err = s.checkOrCreateDir(filepath.Join(s.config.RemoteDir, file.Name()))
				if err != nil {
					return err
				}
			} else {
				localFilePath := filepath.Join(s.config.LocalDir, file.Name())
				_, err := os.Stat(localFilePath)
				if err != nil {
					err = s.downloadFile(filepath.Join(s.config.RemoteDir, file.Name()))
					if err != nil {
						return err
					}
				}
			}
		}
	}

	return nil
}

func (s *SFTP) checkOrCreateDir(path string) error {
	// This checks the existence of a directory, creates it if doesn't exist
	// It also recursively checks and creates subdirectories
	_, err := os.Stat(path)
	if os.IsNotExist(err) {
		errDir := os.MkdirAll(path, 0755)
		if errDir != nil {
			return err
		}
	}
	return nil
}

func (s *SFTP) WatchDirectory() {
	// Starting the worker pool
	for i := 0; i < cap(s.Pool.Tasks); i++ {
		go s.worker()
	}
	logger.Println("Starting initial sync...")
	err := s.initialSync()
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
	go s.watcherWorker(1, events)

	go func() {
		for {
			select {
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				logger.Println("Received event:", event)

				s.Pool.WG.Add(1)
				s.Pool.Tasks <- worker.Task{EventType: event.Op, Name: event.Name}
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
	err = s.AddDirectoriesToWatcher(watcher, s.config.LocalDir)
	if err != nil {
		logger.Fatal(err)
	}

	<-s.ctx.Done()
	logger.Println("Directory watch ended.")
}

func (s *SFTP) AddDirectoriesToWatcher(watcher *fsnotify.Watcher, rootDir string) error {
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
func (s *SFTP) uploadFile(filePath string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	relativePath, err := filepath.Rel(s.config.LocalDir, filePath)
	if err != nil {
		return err
	}

	srcFile, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer func(srcFile *os.File) {
		err = srcFile.Close()
		if err != nil {
			logger.Println("Error closing file:", err)
		}
	}(srcFile)

	dstFile, err := s.Client.Create(filepath.Join(s.config.RemoteDir, relativePath))
	if err != nil {
		return err
	}
	defer func(dstFile *sftp.File) {
		err = dstFile.Close()
		if err != nil {
			logger.Println("Error closing file:", err)
		}
	}(dstFile)

	if s.ctx.Err() != nil {
		return s.ctx.Err()
	}

	_, err = io.Copy(dstFile, srcFile)
	return err
}

func (s *SFTP) downloadFile(remotePath string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	relativePath, err := filepath.Rel(s.config.RemoteDir, remotePath)
	if err != nil {
		return err
	}

	srcFile, err := s.Client.Open(remotePath)
	if err != nil {
		return err
	}
	defer func(srcFile *sftp.File) {
		err = srcFile.Close()
		if err != nil {
			logger.Println("Error closing file:", err)
		}
	}(srcFile)

	dstFile, err := os.Create(filepath.Join(s.config.LocalDir, relativePath))
	if err != nil {
		return err
	}
	defer func(dstFile *os.File) {
		err = dstFile.Close()
		if err != nil {
			logger.Println("Error closing file:", err)
		}
	}(dstFile)

	if s.ctx.Err() != nil {
		return s.ctx.Err()
	}

	_, err = io.Copy(dstFile, srcFile)
	return err
}

func (s *SFTP) Mkdir(dir string) error {

	err := s.Client.Mkdir(filepath.Join(s.config.RemoteDir, dir))
	return err
}
func (s *SFTP) RemoveRemoteFile(remotePath string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	err := s.Client.Remove(remotePath)
	return err
}

func (s *SFTP) RemoveLocalFile(localPath string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	err := os.Remove(localPath)
	return err
}
