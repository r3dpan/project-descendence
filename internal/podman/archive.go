package podman

import (
	"archive/tar"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
)

// TarFile builds a one-file tar archive in memory, ready for PutArchive.
//
// filePath is relative to the archive root and must not be absolute: the
// destination is decided by PutArchive's destPath, and a leading "/" here
// would make the two disagree. Any leading directories are created by libpod
// when the archive is unpacked, so "run/job/backup.sh" needs no separate
// mkdir - which is the reason this delivery mechanism needs nothing on the
// host filesystem at all.
//
// Job scripts are written with mode 0755 so that argv can be the script's own
// path and its shebang can choose the interpreter (task 3.5). The platform
// therefore never needs to know which language a script is written in.
func TarFile(filePath string, mode int64, content []byte) (*bytes.Buffer, error) {
	if filePath == "" {
		return nil, fmt.Errorf("podman: archive file path is empty")
	}
	if path.IsAbs(filePath) {
		return nil, fmt.Errorf("podman: archive file path %q must be relative to the archive root", filePath)
	}
	if filePath != path.Clean(filePath) {
		return nil, fmt.Errorf("podman: archive file path %q is not in canonical form", filePath)
	}
	for _, segment := range strings.Split(filePath, "/") {
		if segment == ".." {
			return nil, fmt.Errorf("podman: archive file path %q escapes the archive root", filePath)
		}
	}

	var buffer bytes.Buffer
	writer := tar.NewWriter(&buffer)

	header := &tar.Header{
		Name:    filePath,
		Mode:    mode,
		Size:    int64(len(content)),
		ModTime: time.Now(),
		// Ownership is stated rather than inherited. A bind mount would have
		// carried the host's uid/gid through the user namespace, which breaks
		// for any image whose process is not root; a tar header simply says
		// what it should be.
		Uid:      0,
		Gid:      0,
		Typeflag: tar.TypeReg,
	}
	if err := writer.WriteHeader(header); err != nil {
		return nil, fmt.Errorf("podman: writing tar header for %s: %w", filePath, err)
	}
	if _, err := writer.Write(content); err != nil {
		return nil, fmt.Errorf("podman: writing tar body for %s: %w", filePath, err)
	}
	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("podman: finishing tar archive: %w", err)
	}

	return &buffer, nil
}

// ArchiveFile is one entry for TarFiles: a path relative to the archive
// root, its mode, and its content.
type ArchiveFile struct {
	Path    string
	Mode    int64
	Content []byte
}

// TarFiles builds a multi-file tar archive in memory, the same way TarFile
// builds a one-file one. Used for a build context (task 4.4): a rendered
// Containerfile plus the language manifest it COPYs in, packed together so
// the build never touches the host filesystem, matching TarFile/PutArchive's
// reasoning for job script delivery (decision #24).
func TarFiles(files []ArchiveFile) (*bytes.Buffer, error) {
	var buffer bytes.Buffer
	writer := tar.NewWriter(&buffer)

	for _, file := range files {
		if file.Path == "" {
			return nil, fmt.Errorf("podman: archive file path is empty")
		}
		if path.IsAbs(file.Path) {
			return nil, fmt.Errorf("podman: archive file path %q must be relative to the archive root", file.Path)
		}
		if file.Path != path.Clean(file.Path) {
			return nil, fmt.Errorf("podman: archive file path %q is not in canonical form", file.Path)
		}
		for _, segment := range strings.Split(file.Path, "/") {
			if segment == ".." {
				return nil, fmt.Errorf("podman: archive file path %q escapes the archive root", file.Path)
			}
		}

		header := &tar.Header{
			Name:     file.Path,
			Mode:     file.Mode,
			Size:     int64(len(file.Content)),
			ModTime:  time.Now(),
			Uid:      0,
			Gid:      0,
			Typeflag: tar.TypeReg,
		}
		if err := writer.WriteHeader(header); err != nil {
			return nil, fmt.Errorf("podman: writing tar header for %s: %w", file.Path, err)
		}
		if _, err := writer.Write(file.Content); err != nil {
			return nil, fmt.Errorf("podman: writing tar body for %s: %w", file.Path, err)
		}
	}
	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("podman: finishing tar archive: %w", err)
	}

	return &buffer, nil
}

// PutArchive unpacks a tar stream inside a container at destPath
// (PUT /libpod/containers/{id}/archive).
//
// This is how a job's script reaches the container it will run in (task 3.5),
// and it is called between create and start - the container's filesystem
// exists from creation, so there is no need to have started it, and starting
// it first would race the entrypoint against the file it is meant to execute.
//
// Chosen over a bind mount deliberately: nothing is written to the host, so
// there is no per-run directory to create, remove, or sweep up after a
// supervisor is SIGKILLed mid-run, and no host path is handed to podman that
// would stop being meaningful if the supervisor were ever containerised or
// the socket were remote.
func (c *Client) PutArchive(ctx context.Context, containerID, destPath string, archive io.Reader) error {
	if containerID == "" {
		return fmt.Errorf("podman: container id is empty")
	}
	if !path.IsAbs(destPath) {
		return fmt.Errorf("podman: archive destination %q must be absolute", destPath)
	}

	endpoint := fmt.Sprintf("/libpod/containers/%s/archive?path=%s", containerID, url.QueryEscape(destPath))

	resp, err := c.doRaw(ctx, c.httpClient, http.MethodPut, endpoint, "application/x-tar", archive)
	if err != nil {
		return fmt.Errorf("podman: putting archive into %s: %w", containerID, err)
	}
	defer resp.Body.Close()

	return checkStatus(resp, "put archive", http.StatusOK)
}
