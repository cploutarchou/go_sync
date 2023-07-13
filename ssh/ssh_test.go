package ssh

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const (
	sshHost     = "localhost"
	sshPort     = "2222"
	sshUser     = "foo"
	sshPassword = "pass"
	localPath   = "/tmp/localDir"
	remotePath  = "/home/foo/upload"
)

func TestNewSyncSSH(t *testing.T) {
	configSSH := Config{
		Host:         sshHost,
		Port:         sshPort,
		Username:     sshUser,
		Password:     sshPassword,
		SyncDir:      LocalToRemote,
		LocalPath:    localPath,
		RemotePath:   remotePath,
		SyncInterval: 1 * time.Second,
	}
	sshSync, err := NewSyncSSH(configSSH)
	require.NoError(t, err)
	require.NotNil(t, sshSync)
	require.NotNil(t, sshSync.Client)

	err = sshSync.Close()
	require.NoError(t, err)
}

func TestSyncSSH_StartSyncAndClose(t *testing.T) {
	configSSH := Config{
		Host:         sshHost,
		Port:         sshPort,
		Username:     sshUser,
		Password:     sshPassword,
		SyncDir:      LocalToRemote,
		LocalPath:    localPath,
		RemotePath:   remotePath,
		SyncInterval: 1 * time.Second,
	}
	sshSync, err := NewSyncSSH(configSSH)
	require.NoError(t, err)
	require.NotNil(t, sshSync)
	require.NotNil(t, sshSync.Client)

	sshSync.StartSync()

	time.Sleep(2 * time.Second)

	err = sshSync.Close()
	require.NoError(t, err)
}
