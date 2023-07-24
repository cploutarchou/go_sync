package sftp

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/user"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

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
	return s.syncDir(s.config.LocalDir, s.config.RemoteDir)
}

func (s *SFTP) syncDir(localDir, remoteDir string) error {
	switch s.Direction {
	case LocalToRemote:
		localFiles, err := os.ReadDir(localDir)
		if err != nil {
			return err
		}
		for _, file := range localFiles {
			localFilePath := filepath.Join(localDir, file.Name())
			remoteFilePath := filepath.Join(remoteDir, file.Name())

			if file.IsDir() {
				err = s.checkOrCreateDir(remoteFilePath)
				if err != nil {
					return err
				}
				err = s.syncDir(localFilePath, remoteFilePath)
				if err != nil {
					return err
				}
			} else {
				_, err := s.Client.Stat(remoteFilePath)
				if err != nil {
					err = s.uploadFile(localFilePath)
					if err != nil {
						return err
					}
				}
			}
		}

	case RemoteToLocal:
		remoteFiles, err := s.Client.ReadDir(remoteDir)
		if err != nil {
			return err
		}

		for _, file := range remoteFiles {
			remoteFilePath := filepath.Join(remoteDir, file.Name())
			localFilePath := filepath.Join(localDir, file.Name())

			if file.IsDir() {
				err = s.checkOrCreateDir(localFilePath)
				if err != nil {
					return err
				}
				err = s.syncDir(localFilePath, remoteFilePath)
				if err != nil {
					return err
				}
			} else {
				_, err := os.Stat(localFilePath)
				if err != nil {
					err = s.downloadFile(remoteFilePath)
					if err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}

func (s *SFTP) checkOrCreateDir(dirPath string) error {
	_, err := os.Stat(dirPath)
	if os.IsNotExist(err) {
		if s.Direction == LocalToRemote {
			//create the directory to remote server if it doesn't exist  and all subdirectories
			err := s.Client.MkdirAll(dirPath)
			if err != nil {
				return err
			}
			// set the permissions to 755
			err = s.Client.Chmod(dirPath, 0755)
			if err != nil {
				return err
			}

		} else {
			errDir := os.MkdirAll(dirPath, 0755)
			if errDir != nil {
				return err
			}
		}
	}
	return nil
}

func (s *SFTP) WatchDirectory() {
	// Starting the worker pool
	for i := 0; i < cap(s.Pool.Tasks); i++ {
		go s.Worker()
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
			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				logger.Println("Error:", err)
			}
		}
	}()

	logger.Println("Adding directories to watcher...")
	switch s.Direction {
	case LocalToRemote:
		logger.Println("Adding watcher to local directory: ", s.config.LocalDir)
		err = s.AddDirectoriesToWatcher(watcher, s.config.LocalDir)
		if err != nil {
			logger.Fatal(err)
		}
		logger.Println("Starting directory watch...")
	case RemoteToLocal:
		logger.Println("Adding watcher to remote directory: ", s.config.RemoteDir)
		err = s.AddDirectoriesToWatcher(watcher, s.config.RemoteDir)
		if err != nil {
			logger.Fatal(err)
		}
		logger.Println("Starting directory watch...")
	}

	<-s.ctx.Done()
	logger.Println("Directory watch ended.")
}

func (s *SFTP) AddDirectoriesToWatcher(watcher *fsnotify.Watcher, rootDir string) error {
	switch s.Direction {
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
			err := s.walkRemoteDir(rootDir, newFiles)
			if err != nil {
				return err
			}

			// Check for new or removed files.
			if prevFiles != nil {
				for p, file := range newFiles {
					prevFile, exists := prevFiles[p]
					if !exists || prevFile.ModTime().Before(file.ModTime()) {

						s.Pool.WG.Add(1)

						s.Pool.Tasks <- worker.Task{EventType: fsnotify.Create, Name: p}
						logger.Println("New or modified file:", p)
					}
				}
				for p := range prevFiles {
					_, exists := newFiles[p]
					if !exists {

						s.Pool.WG.Add(1)

						s.Pool.Tasks <- worker.Task{EventType: fsnotify.Remove, Name: p}
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

	if strings.Contains(remotePath, ".swp") {
		return nil
	}
	logger.Println("Downloading file:", remotePath)
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
	relativePath, err := filepath.Rel(s.config.LocalDir, remotePath)
	if err != nil {
		return err
	}
	toRemotePath := filepath.Join(s.config.RemoteDir, relativePath)
	err = s.Client.Remove(toRemotePath)
	return err
}

func (s *SFTP) RemoveLocalFile(localPath string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	toLocalPath := s.convertRemoteToLocalPath(localPath)
	err := os.Remove(toLocalPath)
	return err
}

// walkRemoteDir traverses a remote directory and its subdirectories,
// adding all files it finds to the provided map.
func (s *SFTP) walkRemoteDir(dir string, files map[string]os.FileInfo) error {
	entries, err := s.Client.ReadDir(dir)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		join := path.Join(dir, entry.Name())
		if entry.IsDir() {
			err = s.walkRemoteDir(join, files)
			if err != nil {
				return err
			}
		} else {
			files[join] = entry

		}
	}

	return nil
}

func (s *SFTP) convertRemoteToLocalPath(remotePath string) string {
	relativePath, _ := filepath.Rel(s.config.RemoteDir, remotePath)
	localPath := filepath.Join(s.config.LocalDir, relativePath)
	return localPath
}

// Worker starts a new worker goroutine.
func (s *SFTP) Worker() {
	for task := range s.Pool.Tasks {
		switch task.EventType {
		case fsnotify.Create:
			switch s.Direction {
			case LocalToRemote:
				err := s.uploadFile(task.Name)
				if err != nil {
					logger.Println("Error uploading file:", err)
				}
			case RemoteToLocal:
				err := s.downloadFile(task.Name)
				if err != nil {
					logger.Println("Error downloading file:", err)
				}
			}
		case fsnotify.Write:
			err := s.uploadFile(task.Name)
			if err != nil {
				logger.Println("Error uploading file:", err)
			}
		case fsnotify.Remove:
			switch s.Direction {
			case LocalToRemote:
				err := s.RemoveRemoteFile(task.Name)
				if err != nil {
					logger.Println("Error deleting file:", err)
				}
			case RemoteToLocal:
				err := s.RemoveLocalFile(task.Name)
				if err != nil {
					logger.Println("Error removing remote file:", err)
				}
			}
		}
		s.Pool.WG.Done()
	}
}
