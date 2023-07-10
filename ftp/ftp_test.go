package ftp_test

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/cploutarchou/go_sync/ftp"
	"github.com/stretchr/testify/require"
)

const (
	ftpHost     = "127.0.0.1"
	ftpPort     = "21"
	ftpUser     = "myuser"
	ftpPassword = "mypass"
)

func TestWatchDirectory(t *testing.T) {
	configFTP := ftp.Config{
		Host:     ftpHost,
		Port:     ftpPort,
		Username: ftpUser,
		Password: ftpPassword,
	}
	ftpClient, err := ftp.New(configFTP)
	if err != nil {
		t.Fatal("Error creating FTP client")
	}
	require.NotNil(t, ftpClient)
	require.NotNil(t, ftpClient.Client)

	// Create a dummy directory
	dirName := "test_dir"
	err = os.Mkdir(dirName, 0755)
	require.NoError(t, err)

	// Launch the goroutine
	go ftpClient.WatchDirectory(dirName, "/")

	time.Sleep(2 * time.Second)

	// Create multiple files in the directory
	for i := 0; i < 10; i++ {
		fileName := fmt.Sprintf("%s/testfile%d.txt", dirName, i)
		f, err := os.Create(fileName)
		// Write some text line-by-line to file
		for j := 0; j < 10; j++ {
			_, err = f.WriteString(fmt.Sprintf("Hello world %d\n", j))
		}
		require.NoError(t, err)
		f.Close()
	}
	//wait for 10 seconds to upload the files

	time.Sleep(10 * time.Second)

	// Cancel the context
	ftpClient.Close()

	// Remove the directory
	err = os.RemoveAll(dirName)
	require.NoError(t, err)

}
