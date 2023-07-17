package sftp

import (
	"fmt"
	"io"
	"log"
	"os"
	"testing"
	"time"

	"github.com/ory/dockertest"
	"github.com/ory/dockertest/docker"
	"github.com/pkg/sftp"
)

func setupSftpServer(t *testing.T) (string, int, *dockertest.Resource) {
	log.Println("Setting up SFTP server...")
	pool, err := dockertest.NewPool("")
	if err != nil {
		t.Fatalf("Could not connect to docker: %s", err)
	}

	options := &dockertest.RunOptions{
		Repository: "atmoz/sftp",
		Tag:        "latest",
		Cmd:        []string{"foo:pass:1001::/home/foo/upload"},
	}

	options.ExposedPorts = []string{"22/tcp"}

	options.PortBindings = map[docker.Port][]docker.PortBinding{
		"22/tcp": {{HostIP: "0.0.0.0", HostPort: "22"}},
	}

	resource, err := pool.RunWithOptions(options)
	if err != nil {
		t.Fatalf("Could not start resource: %s", err)
	}
	time.Sleep(10 * time.Second)
	return "0.0.0.0", 22, resource
}

func teardownSftpServer(t *testing.T, resource *dockertest.Resource) {
	log.Println("Tearing down SFTP server...")
	if err := resource.Close(); err != nil {
		t.Fatalf("Could not stop resource: %s", err)
	}
}

func TestSftpUploadAndDownload(t *testing.T) {
	var (
		err        error
		sftpClient *sftp.Client
	)
	address, port, resource := setupSftpServer(t)
	defer teardownSftpServer(t, resource)
	time.Sleep(10 * time.Second)

	config := &ExtraConfig{
		Username:   "foo",
		Password:   "pass",
		LocalDir:   "./tmp",
		RemoteDir:  "/home/foo/upload",
		Retries:    3,
		MaxRetries: 3,
	}

	conn, err := Connect(address, port, LocalToRemote, config)
	sftpClient = conn.Client
	// Ensure to close the client at the end
	defer func(sftpClient *sftp.Client) {
		err = sftpClient.Close()
		if err != nil {
			t.Fatalf("Failed to close client: %s", err)
		}
	}(sftpClient)

	// Create a sample file for upload
	fileContent := []byte("Hello SFTP!")
	err = os.WriteFile("test.txt", fileContent, 0644)
	if err != nil {
		t.Errorf("Failed to write to file: %s", err)
	}

	srcFile, err := os.Open("test.txt")
	if err != nil {
		t.Errorf("Failed to open file: %s", err)
	}

	defer func(srcFile *os.File) {
		err := srcFile.Close()
		if err != nil {
			t.Fatalf("Failed to close file: %s", err)
		}
	}(srcFile)
	// Check if the upload directory exists

	// List the files in the /home directory
	files, err := sftpClient.ReadDir("/home")
	if err != nil {
		t.Fatalf("Failed to read directory: %s", err)
	}
	for _, file := range files {
		t.Logf("Found directory in /home: %s", file.Name())
	}

	_, err = sftpClient.Stat("/home/foo/upload")
	if err != nil {
		t.Logf("Directory does not exist: %s", err)
		// Try to create the directory
		err = sftpClient.Mkdir("/home/foo/upload")
		if err != nil {
			t.Fatalf("Failed to create directory: %s", err)
		}
	}

	// Upload the source file to remote server
	destFile, err := sftpClient.Create("/home/foo/upload/test.txt")
	if err != nil {
		t.Fatalf("Failed to create file: %s", err)
	}
	if err != nil {
		t.Fatalf("Failed to create file: %s", err)
	}
	_, err = destFile.Write(fileContent)
	if err != nil {
		t.Errorf("Failed to write to file: %s", err)
	}

	err = destFile.Close()
	if err != nil {
		t.Errorf("Failed to close file: %s", err)
	}

	// Now, let's download the file we've just uploaded
	downloadFile, err := sftpClient.Open("/home/foo/upload/test.txt")
	if err != nil {
		t.Errorf("Failed to open file: %s", err)
	}

	defer func(downloadFile *sftp.File) {
		err := downloadFile.Close()
		if err != nil {
			t.Fatalf("Failed to close file: %s", err)
		}
	}(downloadFile)

	// Read the contents of the file
	downloadedFileContent, err := io.ReadAll(downloadFile)
	if err != nil {
		t.Errorf("Failed to read file: %s", err)
	}

	// Check if the downloaded file content matches the source file content
	if string(downloadedFileContent) != string(fileContent) {
		t.Errorf("The content of the downloaded file doesn't match the source file")
	}

	// Delete the file  from the remote server
	err = sftpClient.Remove("/home/foo/upload/test.txt")
	if err != nil {
		t.Errorf("Failed to delete file: %s", err)
	}

	// Delete the file from the local server
	err = os.Remove("test.txt")
	if err != nil {
		t.Errorf("Failed to delete file: %s", err)
	}
	fmt.Println("SFTP test completed successfully!")
}
