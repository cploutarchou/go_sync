package sftp_test

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/cploutarchou/go_sync/sftp"
	"github.com/stretchr/testify/require"
)

const (
	sftpHost     = "127.0.0.1"
	sftpPort     = "2222"
	sftpUser     = "foo"
	sftpPassword = "pass"
)

func TestWatchDirectory(t *testing.T) {
	configSFTP := sftp.Config{
		Host:     sftpHost,
		Port:     sftpPort,
		Username: sftpUser,
		Password: sftpPassword,
	}
	sftpClient, err := sftp.NewSFTP(configSFTP)
	require.NoError(t, err)
	require.NotNil(t, sftpClient)
	require.NotNil(t, sftpClient.Client)

	// Create a dummy directory
	dirName := "test_dir"
	err = os.Mkdir(dirName, 0755)
	require.NoError(t, err)

	// Launch the goroutine
	// Launch the goroutine
	go sftpClient.WatchDirectory(dirName, "/home/foo/upload")

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
	err = sftpClient.Close()
	require.NoError(t, err)

	// Remove the directory
	err = os.RemoveAll(dirName)
	require.NoError(t, err)
}
