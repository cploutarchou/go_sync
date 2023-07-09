package ftp

import (
	"context"
	"github.com/docker/go-connections/nat"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"log"
	"os"
	"testing"
	"time"
)

const (
	ftpServerHost     = "localhost"
	ftpServerPort     = "21"
	ftpServerUsername = "myuser"
	ftpServerPassword = "mypass"
	testFilePath      = "test_file.txt"
)

func TestUploadFile(t *testing.T) {
	// Initialize a new context.
	ctx := context.Background()

	// Configure and start an FTP server container.
	req := testcontainers.ContainerRequest{
		Image:        "fauria/vsftpd",
		ExposedPorts: []string{"20/tcp", "21/tcp", "21100-21110/tcp"},
		Env: map[string]string{
			"FTP_USER":      "myuser",
			"FTP_PASS":      "mypass",
			"PASV_ADDRESS":  "127.0.0.1",
			"PASV_MIN_PORT": "21100",
			"PASV_MAX_PORT": "21110",
		},
		WaitingFor: wait.NewHTTPStrategy("/").WithStartupTimeout(30 * time.Second),
	}
	ftpC, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		log.Fatalf("Failed to start FTP container: %v", err)
	}

	host, err := ftpC.Host(ctx)
	if err != nil {
		log.Fatalf("Failed to get FTP container host: %v", err)
	}

	port, err := ftpC.MappedPort(ctx, nat.Port("21"))
	if err != nil {
		log.Fatalf("Failed to get FTP container port: %v", err)
	}

	defer func() {
		if err := ftpC.Terminate(ctx); err != nil {
			log.Fatalf("Failed to terminate FTP container: %v", err)
		}
	}()

	ftp := New(Config{
		Host:     host,
		Port:     port.Port(),
		Username: "myuser",
		Password: "mypass",
	})

	// Defer client closure
	defer ftp.Client.Close()

	localFile := "test_file.txt"
	remoteFile := "test_file.txt"

	if err := createLocalTestFile(localFile, "This is a test file content."); err != nil {
		log.Fatalf("Failed to create local test file: %v", err)
	}

	defer os.Remove(localFile)

	ftp.uploadFile(localFile, remoteFile)

	// Check if file was uploaded
	files, err := ftp.Client.ReadDir("/")
	if err != nil {
		log.Fatalf("Error reading remote directory: %v", err)
	}

	var uploaded bool
	for _, file := range files {
		if file.Name() == remoteFile {
			uploaded = true
		}
	}

	if !uploaded {
		t.Fatalf("file %s was not uploaded correctly", remoteFile)
	}
}

func createLocalTestFile(filename, content string) error {
	file, err := os.Create(filename)
	if err != nil {
		return err
	}

	defer file.Close()

	_, err = file.WriteString(content)
	if err != nil {
		return err
	}

	// Save changes to file.
	err = file.Sync()
	if err != nil {
		return err
	}

	return nil
}
