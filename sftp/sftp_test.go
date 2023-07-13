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
	sftpHost     = "localhost"
	sftpPort     = "2222"
	sftpUser     = "foo"
	sftpPassword = "pass"
)

func TestSyncLocalToRemote(t *testing.T) {
	config := sftp.Config{
		Host:         sftpHost,
		Port:         sftpPort,
		Username:     sftpUser,
		Password:     sftpPassword,
		SyncDir:      sftp.LocalToRemote,
		LocalPath:    "./test_dir",
		RemotePath:   "/home/foo/upload",
		SyncInterval: time.Minute * 30,
	}

	s, err := sftp.NewSFTP(config)
	require.NoError(t, err)
	defer s.Close()

	// Create test directory
	err = os.Mkdir(config.LocalPath, 0755)
	require.NoError(t, err)

	// Create multiple files in the directory
	for i := 0; i < 10; i++ {
		fileName := fmt.Sprintf("%s/testfile%d.txt", config.LocalPath, i)
		f, err := os.Create(fileName)
		require.NoError(t, err)
		// Write some text line-by-line to file
		for j := 0; j < 10; j++ {
			_, err = f.WriteString(fmt.Sprintf("Hello world %d\n", j))
		}
		require.NoError(t, err)
		f.Close()
	}

	s.StartSync()

	// Allow the sync time to occur
	time.Sleep(10 * time.Second)

	// Check that the files have been uploaded

	// Cleanup
	err = os.RemoveAll(config.LocalPath)
	require.NoError(t, err)
}
