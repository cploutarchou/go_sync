# FTP and SFTP Packages

These Go packages provide functionality for interacting with FTP and SFTP servers. The packages are designed to make it easy to perform file transfers and other operations over FTP and SFTP protocols in Go applications.

## Features

- FTP Package:
  - Connect to an FTP server
  - Watch a directory for changes on the FTP server:
    - Upload files to the FTP server
	- Download files from the FTP server
	- Delete files from the FTP server
	- Update files on the FTP server


- SFTP Package:
  - Connect to an SFTP server (using SSH or public key authentication)
  - Watch a directory for changes on the SFTP server:
	- Upload files to the SFTP server
	- Download files from the SFTP server
	- Delete files from the SFTP server
	- Update files on the SFTP server


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

	"github.com/cploutarchou/syncpkg/sftp"
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
	ftpClient, err := sftp.Connect(
		"127.0.0.1",
		21,
		sftp.RemoteToLocal,
		&sftp.ExtraConfig{
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

