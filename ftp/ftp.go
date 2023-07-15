package ftp

import (
	"bufio"
	"fmt"
	"github.com/fsnotify/fsnotify"
	"io"
	"log"
	"net"
	"net/textproto"
	"os"
	"path/filepath"
)

type SyncDirection int

const (
	LocalToRemote SyncDirection = iota
	RemoteToLocal
)

type FTP struct {
	conn      *textproto.Conn
	Direction SyncDirection
}
type ExtraConfig struct {
	Username string
	Password string
}

func Connect(address string, port int, direction SyncDirection, config *ExtraConfig) (*FTP, error) {
	if port == 0 {
		return nil, fmt.Errorf("port cannot be 0")
	}
	address = net.JoinHostPort(address, fmt.Sprintf("%d", port))
	conn, err := net.Dial("tcp", address)
	if err != nil {
		return nil, err
	}

	ftp := &FTP{
		conn:      textproto.NewConn(conn),
		Direction: direction,
	}

	if config != nil {
		err = ftp.Login(config.Username, config.Password)
		if err != nil {
			return nil, err
		}
	} else {
		err = ftp.Login("anonymous", "anonymous")
		if err != nil {
			return nil, err
		}
	}
	return ftp, nil
}

func (c *FTP) Login(username, password string) error {
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

func (c *FTP) WatchDirectory(dir string) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Fatal(err)
	}
	defer func(watcher *fsnotify.Watcher) {
		_ = watcher.Close()
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

	err = watcher.Add(dir)
	if err != nil {
		log.Fatal(err)
	}

	<-make(chan struct{})
}

func (c *FTP) uploadFile(filePath string) error {
	file, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer func(file *os.File) {
		_ = file.Close()
	}(file)

	_, err = c.conn.Cmd("PASV")
	if err != nil {
		return err
	}

	_, err = c.conn.Cmd("STOR %s", filepath.Base(filePath))
	if err != nil {
		return err
	}

	_, err = io.Copy(c.conn.W, file)
	if err != nil {
		return err
	}

	return nil
}

func (c *FTP) downloadFile(name string) error {
	_, err := c.conn.Cmd("PASV")
	if err != nil {
		return err
	}

	_, err = c.conn.Cmd("RETR %s", name)
	if err != nil {
		return err
	}

	file, err := os.Create(name)
	if err != nil {
		return err
	}
	defer func(file *os.File) {
		_ = file.Close()
	}(file)

	_, err = io.Copy(file, c.conn.R)
	if err != nil {
		return err
	}

	return nil
}
