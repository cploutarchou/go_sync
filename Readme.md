# Bi-Directional Sync Packages for FTP and SFTP

The Bi-Directional Sync packages for FTP and SFTP offer a powerful solution for synchronizing specific folders bidirectionally, enabling seamless file transfers and operations over FTP and SFTP protocols in Go applications. With the integration of the Watcher *fsnotify.Watcher, these packages provide real-time updates, deletions, and creations of files.
## Features

- FTP Package:
  - Connect to an FTP server
  - Bi-Directional Sync: Effortlessly synchronize specific local and remote folders bidirectionally.
  - Real-Time Watcher: Utilize *fsnotify.Watcher to monitor changes in the target directories and update accordingly:
    - Upload files to the FTP server
	- Download files from the FTP server
	- Delete files from the FTP server
	- Update files on the FTP server


- SFTP Package:
  - Connect to an SFTP server (using SSH or public key authentication)
  - Bi-Directional Sync: Effortlessly synchronize specific local and remote folders bidirectionally.
  - Real-Time Watcher: Utilize *fsnotify.Watcher to monitor changes in the target directories and update accordingly:
	  - Upload files to the FTP server
	  - Download files from the FTP server
	  - Delete files from the FTP server
	  - Update files on the FTP server

## Installation

To use the packages in your Go application, you can install them using `go get`:

```bash
go get github.com/cploutarchou/syncpkg/ftp
```

```bash
go get github.com/cploutarchou/syncpkg/sftp
```

## Usage

### FTP Package

The following example demonstrates how to use the FTP package to connect to an FTP server and monitor a directory for changes on the p

```go
package main

import (
	"os"

	"github.com/cploutarchou/syncpkg/ftp"
)

func main() {
	// Get the current working directory
	dir, err := os.Getwd()
	if err != nil {
		panic(err)
	}
	// Set the local directory to the sample_data directory
	dir = dir + "/sample_data"
	// Connect to the FTP server
	ftpClient, err := ftp.Connect(
		"127.0.0.1",
		21,
		ftp.RemoteToLocal,
		&ftp.ExtraConfig{
			Username:   "yourusername",
			Password:   "yourpassword",
			LocalDir:   dir,
			RemoteDir:  "/home/chris/test",
			Retries:    3,
			MaxRetries: 5,
		})

	if err != nil {
		panic(err)
	}
	// Watch the directory for changes
	ftpClient.WatchDirectory()
}
```

### SFTP Package

The following example demonstrates how to use the SFTP package to connect to an SFTP server and monitor a directory for changes on the p
     
#### Using SSH Authentication
```go
package main

import (
	"os"

	s "github.com/cploutarchou/syncpkg/sftp"
)

func main() {

	dir, err := os.Getwd()
	if err != nil {
		panic(err)
	}
	dir = dir + "/tmp"
	client, _ := s.Connect("127.0.0.1", 22, s.LocalToRemote, &s.ExtraConfig{
		Username:   "foo",
		Password:   "pass",
		LocalDir:   dir,
		RemoteDir:  "/",
		Retries:    3,
		MaxRetries: 3,
	})
	go client.WatchDirectory()
}
```
##### Using Public Key Authentication
```go
package main

import (
	"os"

	s "github.com/cploutarchou/syncpkg/sftp"
)

func main() {

	dir, err := os.Getwd()
	if err != nil {
		panic(err)
	}
	dir = dir + "/tmp"
	client, _ := s.ConnectSSHPair("127.0.0.1", 22, s.LocalToRemote, &s.ExtraConfig{
		Username:   "foo",
		Password:   "",
		LocalDir:   dir,
		RemoteDir:  "/",
		Retries:    3,
		MaxRetries: 3,
	})
	go client.WatchDirectory()
}

```

## License

This project is licensed under the MIT License - see the [LICENSE](LICENSE) file for details
