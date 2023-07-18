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

func (c *FTP) Login(username, password string) error {
	c.Lock()
	defer c.Unlock()

	_, err := c.conn.Cmd("USER %s", username)
	if err != nil {
		return err
	}

	_, err = c.conn.Cmd("PASS %s", password)
	if err != nil {
		return err
	}

	return nil
}

func (c *FTP) List() ([]string, error) {
	c.Lock()
	defer c.Unlock()

	_, err := c.conn.Cmd("PASV")
	if err != nil {
		return nil, err
	}

	_, err = c.conn.Cmd("LIST")
	if err != nil {
		return nil, err
	}

	reader := bufio.NewReader(c.conn.R)
	line, _, err := reader.ReadLine()
	var files []string
	for err == nil {
		files = append(files, string(line))
		line, _, err = reader.ReadLine()
	}

	return files, nil
}

func (c *FTP) WatchDirectory() {
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
				if event.Op&fsnotify.Remove == fsnotify.Remove {
					log.Println("Deleted file:", event.Name)
					if c.Direction == LocalToRemote {
						err := c.removeRemoteFile(event.Name)
						if err != nil {
							log.Println("Error removing remote file:", err)
						}
					}
					if c.Direction == RemoteToLocal {
						err := c.removeLocalFile(event.Name)
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

	err = watcher.Add(c.config.LocalDir)
	if err != nil {
		log.Fatal(err)
	}

	<-make(chan struct{})
}
func (c *FTP) uploadFile(filePath string) error {
	c.Lock()
	defer c.Unlock()

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

	for i := 0; i < c.config.MaxRetries; i++ {
		_, err = c.conn.Cmd("PASV")
		if err != nil {
			if i == c.config.MaxRetries-1 {
				return err
			}
			continue
		}

		_, err = c.conn.Cmd("STOR %s", filepath.Join(c.config.RemoteDir, filepath.Base(filePath)))
		if err != nil {
			if i == c.config.MaxRetries-1 {
				return err
			}
			continue
		}

		_, err = io.Copy(c.conn.W, file)
		if err != nil {
			if i == c.config.MaxRetries-1 {
				return err
			}
			continue
		}

		break
	}

	return nil
}

func (c *FTP) downloadFile(name string) error {
	c.Lock()
	defer c.Unlock()

	file, err := os.Create(filepath.Join(c.config.LocalDir, name))
	if err != nil {
		return err
	}
	defer func(file *os.File) {
		err := file.Close()
		if err != nil {
			log.Println("Error closing file:", err)
		}
	}(file)

	for i := 0; i < c.config.MaxRetries; i++ {
		_, err = c.conn.Cmd("PASV")
		if err != nil {
			if i == c.config.MaxRetries-1 {
				return err
			}
			continue
		}

		_, err = c.conn.Cmd("RETR %s", filepath.Join(c.config.RemoteDir, name))
		if err != nil {
			if i == c.config.MaxRetries-1 {
				return err
			}
			continue
		}

		_, err = io.Copy(file, c.conn.R)
		if err != nil {
			if i == c.config.MaxRetries-1 {
				return err
			}
			continue
		}

		break
	}

	return nil
}

func (c *FTP) Stat(path string) (string, error) {
	c.Lock()
	defer c.Unlock()

	_, err := c.conn.Cmd("PASV")
	if err != nil {
		return "", err
	}

	_, err = c.conn.Cmd("LIST %s", path)
	if err != nil {
		return "", err
	}

	reader := bufio.NewReader(c.conn.R)
	line, _, err := reader.ReadLine()
	if err != nil && err != io.EOF {
		return "", err
	}

	return string(line), nil
}

func (c *FTP) Mkdir(dir string) error {
	c.Lock()
	defer c.Unlock()

	_, err := c.conn.Cmd("MKD %s", dir)
	if err != nil {
		return err
	}

	return nil
}

func (c *FTP) removeRemoteFile(filePath string) error {
	c.Lock()
	defer c.Unlock()

	_, err := c.conn.Cmd("DELE %s", filepath.Join(c.config.RemoteDir, filepath.Base(filePath)))
	if err != nil {
		return err
	}

	return nil
}

func (c *FTP) removeLocalFile(filePath string) error {
	c.Lock()
	defer c.Unlock()

	err := os.Remove(filePath)
	if err != nil {
		return err
	}

	return nil
}
