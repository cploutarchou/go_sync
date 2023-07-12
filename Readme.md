# SSH, FTP, and SFTP Syncing Package

This package provides a unified solution to sync files/directories via SSH, FTP, and SFTP protocols. It's designed with user-friendly APIs, which allow you to establish a connection, initiate the synchronization, and manage the connection lifecycle.

## Installation

Use `go get` to add this package to your project.

```bash
go get github.com/yourusername/syncpkg
```
## Configuration
The syncing direction, local/remote paths, and the sync interval are all configurable.

Here's a sample configuration:

```go
config := Config{
	Host:         "localhost",
	Port:         "22",
	Username:     "user",
	Password:     "password",
	SyncDir:      LocalToRemote,
	LocalPath:    "/path/to/local/directory",
	RemotePath:   "/path/to/remote/directory",
	SyncInterval: 5 * time.Minute,
}
```
## Usage
Here is a simple usage example:

```go
// Create new instance
sync, err := NewSyncSSH(config)
if err != nil {
	log.Fatal(err)
}

// Start syncing
sync.StartSync()

// Close when done
defer sync.Close()
```

## Package Structure
### SyncSSH
SyncSSH is the main struct that handles the synchronization tasks. It maintains the SSH connection and context for synchronization operations.

### Config
Config struct is used to configure the SyncSSH instance. It includes SSH credentials, sync paths, and the sync interval.

### SyncDirection
SyncDirection is an enum-like type that defines the sync direction - local to remote or remote to local.

### Error Handling
Errors during synchronization are logged and don't interrupt the ongoing sync operation. This allows the syncing to be resilient against temporary network issues or file system inconsistencies.

### Cleanup
Always close the SyncSSH instance when you're done with it to ensure all resources are freed.

### Contributing
Pull requests are welcome. For major changes, please open an issue first to discuss what you would like to change.

Please make sure to update tests as appropriate.
