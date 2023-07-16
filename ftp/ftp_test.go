package ftp

import (
	"github.com/ory/dockertest"
	"log"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func setupFtpServer(t *testing.T) (string, *dockertest.Resource) {
	log.Println("Setting up FTP server...")
	pool, err := dockertest.NewPool("")
	if err != nil {
		t.Fatalf("Could not connect to docker: %s", err)
	}

	resource, err := pool.Run("stilliard/pure-ftpd", "latest", []string{"FTP_USER_NAME=foo", "FTP_USER_PASS=pass", "FTP_USER_HOME=/home/foo"})
	if err != nil {
		t.Fatalf("Could not start resource: %s", err)
	}

	address := "localhost:"
	if err = pool.Retry(func() error {
		log.Println("Attempting to connect to FTP server...")
		ftp, err := Connect(address, 21, LocalToRemote, &ExtraConfig{
			Username: "foo",
			Password: "pass",
		})
		if err != nil {
			return err
		}

		defer ftp.conn.Close()
		return nil
	}); err != nil {
		t.Fatalf("Could not connect to docker: %s", err)
	}

	log.Println("FTP server started")
	return address, resource
}

func teardownFtpServer(t *testing.T, resource *dockertest.Resource) {
	log.Println("Tearing down FTP server...")
	if err := resource.Close(); err != nil {
		t.Fatalf("Could not stop resource: %s", err)
	}
}

func TestLogin(t *testing.T) {
	log.Println("Running TestLogin...")
	address, resource := setupFtpServer(t)
	defer teardownFtpServer(t, resource)
	time.Sleep(10 * time.Second)

	log.Printf("Connecting to FTP server at address %s...\n", address)
	ftp, err := Connect(address, 21, LocalToRemote, &ExtraConfig{
		Username: "foo",
		Password: "pass",
	})

	if err != nil {
		t.Fatalf("Connect returned an error: %v", err)
	}

	if ftp == nil {
		t.Fatalf("Connect returned nil FTP")
	}

	log.Println("TestLogin completed successfully.")
}

func TestWatchDirectory(t *testing.T) {
	log.Println("Running TestWatchDirectory...")
	address, resource := setupFtpServer(t)
	defer teardownFtpServer(t, resource)

	conf := &ExtraConfig{
		Username:   "foo",
		Password:   "pass",
		Retries:    3,
		MaxRetries: 3,
		RemoteDir:  "/home/foo/upload",
		LocalDir:   "./tmp",
	}

	log.Printf("Connecting to FTP server at address %s...\n", address)
	ftpClient, err := Connect(address, 21, LocalToRemote, conf)
	if err != nil {
		t.Fatalf("Connect returned an error: %v", err)
	}

	if ftpClient == nil {
		t.Fatalf("Connect returned nil FTP")
	}

	dirToWatch := "./tmp"
	os.MkdirAll(dirToWatch, os.ModePerm)
	log.Printf("Created directory to watch: %s\n", dirToWatch)

	go ftpClient.WatchDirectory()

	time.Sleep(20 * time.Second)

	fileName := "test.txt"
	filePath := filepath.Join(dirToWatch, fileName)

	log.Printf("Creating file: %s\n", filePath)
	file, err := os.Create(filePath)
	if err != nil {
		t.Fatalf("Failed to create file: %v", err)
	}
	file.WriteString("test")
	file.Close()
	log.Println("File created and closed.")

	time.Sleep(1 * time.Second)

	remoteFilePath := filepath.Join(conf.RemoteDir, fileName)
	log.Printf("Checking remote file status for: %s\n", remoteFilePath)
	_, err = ftpClient.Stat(remoteFilePath)
	if err != nil {
		t.Fatalf("Failed to stat file: %v", err)
	}

	log.Printf("Removing directory: %s\n", dirToWatch)
	os.RemoveAll(dirToWatch)
	log.Println("Directory removed.")

	time.Sleep(1 * time.Second)

	log.Println("TestWatchDirectory completed successfully.")
}
