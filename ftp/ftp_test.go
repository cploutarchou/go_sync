package ftp

import (
	"github.com/goftp/server"
	"io"
	"log"
	"testing"
	"time"
)

type TestDriverFactory struct{}

type TestDriver struct{}

func (driver *TestDriver) Init(*server.Conn) {}

func (driver *TestDriver) Stat(path string) (server.FileInfo, error) {
	return nil, nil
}

func (driver *TestDriver) ChangeDir(path string) error {
	return nil
}

func (driver *TestDriver) ListDir(path string, callback func(server.FileInfo) error) error {
	return nil
}

func (driver *TestDriver) DeleteDir(path string) error {
	return nil
}

func (driver *TestDriver) DeleteFile(path string) error {
	return nil
}

func (driver *TestDriver) Rename(fromPath, toPath string) error {
	return nil
}

func (driver *TestDriver) MakeDir(path string) error {
	return nil
}

func (driver *TestDriver) GetFile(path string, offset int64) (int64, io.ReadCloser, error) {
	return 0, nil, nil
}

func (driver *TestDriver) PutFile(destPath string, data io.Reader, appendData bool) (int64, error) {
	return 0, nil
}

func (factory *TestDriverFactory) NewDriver() (server.Driver, error) {
	return &TestDriver{}, nil
}

type FTPServer struct {
	server *server.Server
}

func NewFTPServer() *FTPServer {
	opts := &server.ServerOpts{
		Factory:        &TestDriverFactory{},
		Port:           2121, // Change this line
		Hostname:       "127.0.0.1",
		Auth:           &server.SimpleAuth{Name: "foo", Password: "pass"},
		Name:           "SyncFTP",
		WelcomeMessage: "Welcome to SyncFTP",
		PassivePorts:   "3000-3009",
	}
	return &FTPServer{
		server: server.NewServer(opts),
	}
}

func (ftp *FTPServer) StartServer(t *testing.T) {
	// Start listening on the configured port
	err := ftp.server.ListenAndServe()
	if err != nil && err.Error() != "ftp: Server closed" {
		t.Fatalf("FTP server failed to start: %v", err)
	}
}

func (ftp *FTPServer) StopServer() {
	ftp.server.Shutdown()
	log.Println("FTP server has been stopped.")
}

// TestLogin tests the Login function by attempting to login with a mock FTP server.
func TestLogin(t *testing.T) {
	// Starting mock FTP server.
	ftp := NewFTPServer()

	go ftp.StartServer(t)

	time.Sleep(10 * time.Second)
	log.Println("FTP server is up and running.")

	conf := &ExtraConfig{
		Username: "foo",
		Password: "pass",
	}
	ftp_, err := Connect("127.0.0.1", 2121, LocalToRemote, conf)

	time.Sleep(10 * time.Second) // Add a more significant delay before shutting down the server

	ftp.StopServer()

	if err != nil {
		if err.Error() == "ftp: Server closed" {
			log.Println("Server was closed as expected.")
		} else {
			t.Fatalf("Connect returned an error: %v", err)
		}
	}

	if ftp_ == nil {
		t.Fatalf("Connect returned nil FTP")
	}
}
