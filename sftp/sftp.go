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
	}, nil
}

func ConnectSSHPair(address string, port int, direction SyncDirection, config *ExtraConfig) (*SFTP, error) {
	usr, err := user.Current()
	if err != nil {
		return nil, fmt.Errorf("cannot get user home directory: %v", err)
	}

	key, err := os.ReadFile(filepath.Join(usr.HomeDir, ".ssh", "id_rsa"))
	if err != nil {
		return nil, fmt.Errorf("unable to read private key: %v", err)
	}

	signer, err := ssh.ParsePrivateKey(key)
	if err != nil {
		return nil, fmt.Errorf("unable to parse private key: %v", err)
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
	}, nil
}

func (s *SFTP) initialSync() error {
	s.Lock()
	defer s.Unlock()

	switch s.Direction {
	case LocalToRemote:
		localFiles, err := os.ReadDir(s.config.LocalDir)
		if err != nil {
			return err
		}

		for _, file := range localFiles {
			err = s.uploadFile(filepath.Join(s.config.LocalDir, file.Name()))
			if err != nil {
				return err
			}
		}

	case RemoteToLocal:
		remoteFiles, err := s.Client.ReadDir(s.config.RemoteDir)
		if err != nil {
			return err
		}

		for _, file := range remoteFiles {
			err = s.downloadFile(filepath.Join(s.config.RemoteDir, file.Name()))
			if err != nil {
				return err
			}
		}
	}

	return nil
}

func (s *SFTP) WatchDirectory() {
	err := s.initialSync()
	if err != nil {
		log.Fatal(err)
	}
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Fatal(err)
	}
	defer func(watcher *fsnotify.Watcher) {
		err = watcher.Close()
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
					if s.Direction == LocalToRemote {
						err := s.uploadFile(event.Name)
						if err != nil {
							log.Println("Error uploading file:", err)
						}
					}
					if s.Direction == RemoteToLocal {
						err := s.downloadFile(event.Name)
						if err != nil {
							log.Println("Error downloading file:", err)
						}
					}
				}
				if event.Op&fsnotify.Remove == fsnotify.Remove {
					log.Println("Deleted file:", event.Name)
					if s.Direction == LocalToRemote {
						err := s.RemoveRemoteFile(event.Name)
						if err != nil {
							log.Println("Error removing remote file:", err)
						}
					}
					if s.Direction == RemoteToLocal {
						err := s.RemoveLocalFile(event.Name)
						if err != nil {
							log.Println("Error removing local file:", err)
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

	err = watcher.Add(s.config.LocalDir)
	if err != nil {
		log.Fatal(err)
	}

	<-s.ctx.Done()
	log.Println("Directory watch ended.")
}

func (s *SFTP) uploadFile(filePath string) error {
	s.Lock()
	defer s.Unlock()

	srcFile, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer func(srcFile *os.File) {
		err = srcFile.Close()
		if err != nil {
			log.Println("Error closing file:", err)
		}
	}(srcFile)

	dstFile, err := s.Client.Create(filepath.Join(s.config.RemoteDir, filepath.Base(filePath)))
	if err != nil {
		return err
	}
	defer func(dstFile *sftp.File) {
		err = dstFile.Close()
		if err != nil {
			log.Println("Error closing file:", err)
		}
	}(dstFile)

	if s.ctx.Err() != nil {
		return s.ctx.Err()
	}

	_, err = io.Copy(dstFile, srcFile)
	return err
}

func (s *SFTP) downloadFile(remotePath string) error {
	s.Lock()
	defer s.Unlock()

	srcFile, err := s.Client.Open(remotePath)
	if err != nil {
		return err
	}
	defer func(srcFile *sftp.File) {
		err = srcFile.Close()
		if err != nil {
			log.Println("Error closing file:", err)
		}
	}(srcFile)

	dstFile, err := os.Create(filepath.Join(s.config.LocalDir, filepath.Base(remotePath)))
	if err != nil {
		return err
	}
	defer func(dstFile *os.File) {
		err = dstFile.Close()
		if err != nil {
			log.Println("Error closing file:", err)
		}
	}(dstFile)

	if s.ctx.Err() != nil {
		return s.ctx.Err()
	}

	_, err = io.Copy(dstFile, srcFile)
	return err
}

func (s *SFTP) Mkdir(dir string) error {
	s.Lock()
	defer s.Unlock()

	err := s.Client.Mkdir(filepath.Join(s.config.RemoteDir, dir))
	return err
}
func (s *SFTP) RemoveRemoteFile(remotePath string) error {
	s.Lock()
	defer s.Unlock()

	err := s.Client.Remove(remotePath)
	return err
}

func (s *SFTP) RemoveLocalFile(localPath string) error {
	s.Lock()
	defer s.Unlock()

	err := os.Remove(localPath)
	return err
}
