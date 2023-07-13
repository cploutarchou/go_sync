package ssh

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

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

type SyncSSH struct {
	Config
	Client *ssh.Client
	mutex  sync.Mutex
	ctx    context.Context
	cancel context.CancelFunc
}

func NewSyncSSH(config Config) (*SyncSSH, error) {
	client, err := newSSHClient(config)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithCancel(context.Background())

	return &SyncSSH{
		Config: config,
		Client: client,
		ctx:    ctx,
		cancel: cancel,
	}, nil
}

func newSSHClient(config Config) (*ssh.Client, error) {
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

	return client, nil
}

func (s *SyncSSH) StartSync() {
	if s.SyncDir == LocalToRemote {
		go s.sync(s.uploadDirectory, s.LocalPath, s.RemotePath)
	} else {
		go s.sync(s.downloadDirectory, s.RemotePath, s.LocalPath)
	}
}

func (s *SyncSSH) sync(syncFunc func(string, string) error, srcPath, dstPath string) {
	for {
		select {
		case <-time.After(s.SyncInterval):
			err := syncFunc(srcPath, dstPath)
			if err != nil {
				log.Println("sync error: ", err)
			}
		case <-s.ctx.Done():
			return
		}
	}
}

func (s *SyncSSH) uploadDirectory(localDir, remoteDir string) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	return filepath.Walk(localDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return err
		}

		// Calculate remote file path
		relativePath, _ := filepath.Rel(localDir, path)
		remotePath := filepath.Join(remoteDir, relativePath)

		return s.uploadFile(path, remotePath, info)
	})
}

func (s *SyncSSH) uploadFile(localPath, remotePath string, info os.FileInfo) error {
	session, err := s.Client.NewSession()
	if err != nil {
		return fmt.Errorf("new session: %w", err)
	}
	defer session.Close()

	f, err := os.Open(localPath)
	if err != nil {
		return fmt.Errorf("open: %w", err)
	}
	defer f.Close()

	w, err := session.StdinPipe()
	if err != nil {
		return fmt.Errorf("stdin pipe: %w", err)
	}
	defer w.Close()

	go func() {
		fmt.Fprintln(w, "C0644", info.Size(), filepath.Base(localPath))
		io.Copy(w, f)
		fmt.Fprint(w, "\x00")
	}()

	err = session.Run("/usr/bin/scp -qrt " + filepath.Dir(remotePath))
	if err != nil {
		return fmt.Errorf("run: %w", err)
	}

	return nil
}

func (s *SyncSSH) downloadDirectory(remoteDir, localDir string) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	session, err := s.Client.NewSession()
	if err != nil {
		return fmt.Errorf("new session: %w", err)
	}
	defer session.Close()

	r, err := session.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}

	err = session.Start("/usr/bin/scp -r " + remoteDir)
	if err != nil {
		return fmt.Errorf("start: %w", err)
	}

	return s.parseAndDownloadFiles(r, localDir)
}

func (s *SyncSSH) parseAndDownloadFiles(reader io.Reader, localDir string) error {
	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}

		parts := strings.SplitN(line, " ", 3)
		if len(parts) < 3 {
			return fmt.Errorf("parse: invalid response: %s", line)
		}

		filesize, err := strconv.Atoi(parts[1])
		if err != nil {
			return fmt.Errorf("parse: invalid filesize: %s", parts[1])
		}

		localPath := filepath.Join(localDir, parts[2])

		if err = s.downloadFile(reader, localPath, filesize); err != nil {
			return err
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("scan: %w", err)
	}

	if err := s.Client.Wait(); err != nil {
		return fmt.Errorf("wait: %w", err)
	}

	return nil
}

func (s *SyncSSH) downloadFile(reader io.Reader, localPath string, filesize int) error {
	err := os.MkdirAll(filepath.Dir(localPath), os.ModePerm)
	if err != nil {
		return fmt.Errorf("mkdirall: %w", err)
	}

	f, err := os.Create(localPath)
	if err != nil {
		return fmt.Errorf("create: %w", err)
	}
	defer f.Close()

	n, err := io.CopyN(f, reader, int64(filesize))
	if err != nil {
		return fmt.Errorf("copy: %w", err)
	}

	if n != int64(filesize) {
		return fmt.Errorf("copy: expected %d bytes, got %d", filesize, n)
	}

	return nil
}

func (s *SyncSSH) Close() error {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	s.cancel()

	if err := s.Client.Close(); err != nil {
		return err
	}
	return nil
}
