package ftp

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"net"
	"net/textproto"
	"os"
	"path/filepath"
	"sync"

	"github.com/fsnotify/fsnotify"
)

type SyncDirection int

const (
	LocalToRemote SyncDirection = iota
	RemoteToLocal
)

type FTP struct {
	sync.Mutex
	conn      *textproto.Conn
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

func Connect(address string, port int, direction SyncDirection, config *ExtraConfig) (*FTP, error) {
	address = net.JoinHostPort(address, fmt.Sprintf("%d", port))
	conn, err := net.Dial("tcp", address)
	if err != nil {
		return nil, err
	}

	ftp := &FTP{
		conn:      textproto.NewConn(conn),
		Direction: direction,
	}
	ftp.config = config

	if config != nil {
		err = ftp.Login(config.Username, config.Password)
	} else {
		err = ftp.Login("anonymous", "anonymous")
	}
	if err != nil {
		_ = ftp.conn.Close()
		return nil, err
	}
	return ftp, nil
}

func (f *FTP) Login(username, password string) error {
	f.Lock()
	defer f.Unlock()

	_, err := f.conn.Cmd("USER %s", username)
	if err != nil {
		return err
	}

	_, err = f.conn.Cmd("PASS %s", password)
	if err != nil {
		return err
	}

	return nil
}

func (f *FTP) List() ([]string, error) {
	f.Lock()
	defer f.Unlock()

	_, err := f.conn.Cmd("PASV")
	if err != nil {
		return nil, err
	}

	_, err = f.conn.Cmd("LIST")
	if err != nil {
		return nil, err
	}

	reader := bufio.NewReader(f.conn.R)
	line, _, err := reader.ReadLine()
	var files []string
	for err == nil {
		files = append(files, string(line))
		line, _, err = reader.ReadLine()
	}

	return files, nil
}
func (f *FTP) initialSync() error {
	f.Lock()
	defer f.Unlock()

	switch f.Direction {
	case LocalToRemote:
		localFiles, err := os.ReadDir(f.config.LocalDir)
		if err != nil {
			return err
		}

		for _, file := range localFiles {
			err = f.uploadFile(filepath.Join(f.config.LocalDir, file.Name()))
			if err != nil {
				return err
			}
		}

	case RemoteToLocal:
		remoteFiles, err := f.List()
		if err != nil {
			return err
		}

		for _, file := range remoteFiles {
			err = f.downloadFile(file)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

func (f *FTP) WatchDirectory() {
	err := f.initialSync()
	if err != nil {
		log.Fatal(err)
	}
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
					if f.Direction == LocalToRemote {
						err := f.uploadFile(event.Name)
						if err != nil {
							log.Println("Error uploading file:", err)
						}
					}
					if f.Direction == RemoteToLocal {
						err := f.downloadFile(event.Name)
						if err != nil {
							log.Println("Error downloading file:", err)
						}
					}
				}
				if event.Op&fsnotify.Remove == fsnotify.Remove {
					log.Println("Deleted file:", event.Name)
					if f.Direction == LocalToRemote {
						err := f.removeRemoteFile(event.Name)
						if err != nil {
							log.Println("Error removing remote file:", err)
						}
					}
					if f.Direction == RemoteToLocal {
						err := f.removeLocalFile(event.Name)
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

	err = watcher.Add(f.config.LocalDir)
	if err != nil {
		log.Fatal(err)
	}

	<-make(chan struct{})
}
func (f *FTP) uploadFile(filePath string) error {
	f.Lock()
	defer f.Unlock()

	file, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer func(file *os.File) {
		err := file.Close()
		if err != nil {
			log.Println("Error closing file:", err)
		}
	}(file)

	for i := 0; i < f.config.MaxRetries; i++ {
		_, err = f.conn.Cmd("PASV")
		if err != nil {
			if i == f.config.MaxRetries-1 {
				return err
			}
			continue
		}

		_, err = f.conn.Cmd("STOR %s", filepath.Join(f.config.RemoteDir, filepath.Base(filePath)))
		if err != nil {
			if i == f.config.MaxRetries-1 {
				return err
			}
			continue
		}

		_, err = io.Copy(f.conn.W, file)
		if err != nil {
			if i == f.config.MaxRetries-1 {
				return err
			}
			continue
		}

		break
	}

	return nil
}

func (f *FTP) downloadFile(name string) error {
	f.Lock()
	defer f.Unlock()

	file, err := os.Create(filepath.Join(f.config.LocalDir, name))
	if err != nil {
		return err
	}
	defer func(file *os.File) {
		err := file.Close()
		if err != nil {
			log.Println("Error closing file:", err)
		}
	}(file)

	for i := 0; i < f.config.MaxRetries; i++ {
		_, err = f.conn.Cmd("PASV")
		if err != nil {
			if i == f.config.MaxRetries-1 {
				return err
			}
			continue
		}

		_, err = f.conn.Cmd("RETR %s", filepath.Join(f.config.RemoteDir, name))
		if err != nil {
			if i == f.config.MaxRetries-1 {
				return err
			}
			continue
		}

		_, err = io.Copy(file, f.conn.R)
		if err != nil {
			if i == f.config.MaxRetries-1 {
				return err
			}
			continue
		}

		break
	}

	return nil
}

func (f *FTP) Stat(path string) (string, error) {
	f.Lock()
	defer f.Unlock()

	_, err := f.conn.Cmd("PASV")
	if err != nil {
		return "", err
	}

	_, err = f.conn.Cmd("LIST %s", path)
	if err != nil {
		return "", err
	}

	reader := bufio.NewReader(f.conn.R)
	line, _, err := reader.ReadLine()
	if err != nil && err != io.EOF {
		return "", err
	}

	return string(line), nil
}

func (f *FTP) Mkdir(dir string) error {
	f.Lock()
	defer f.Unlock()

	_, err := f.conn.Cmd("MKD %s", dir)
	if err != nil {
		return err
	}

	return nil
}

func (f *FTP) removeRemoteFile(filePath string) error {
	f.Lock()
	defer f.Unlock()

	_, err := f.conn.Cmd("DELE %s", filepath.Join(f.config.RemoteDir, filepath.Base(filePath)))
	if err != nil {
		return err
	}

	return nil
}

func (f *FTP) removeLocalFile(filePath string) error {
	f.Lock()
	defer f.Unlock()

	err := os.Remove(filePath)
	if err != nil {
		return err
	}

	return nil
}
