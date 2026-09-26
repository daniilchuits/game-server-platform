package mojang

import (
	"context"
	"crypto/sha1"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"game-server-platform/internal/domain"
)

// EnsureJar reuses verified jars and publishes downloads only after checking them.
func (client *Client) EnsureJar(ctx context.Context, download domain.ServerDownload, destination string) error {
	complete, err := matchesDownload(destination, download)
	if err != nil {
		return fmt.Errorf("check existing server jar: %w", err)
	}
	if complete {
		return nil
	}
	response, err := client.get(ctx, download.URL)
	if err != nil {
		return fmt.Errorf("download server jar: %w", err)
	}
	defer response.Body.Close()

	file, err := os.CreateTemp(filepath.Dir(destination), ".server-*.part")
	if err != nil {
		return fmt.Errorf("create temporary jar: %w", err)
	}
	defer os.Remove(file.Name())
	defer file.Close()

	// SHA-1 matches Mojang's metadata; it detects incomplete or damaged downloads.
	digest := sha1.New()
	written, err := io.Copy(io.MultiWriter(file, digest), response.Body)
	if err != nil {
		return fmt.Errorf("save server jar: %w", err)
	}
	if written != download.Size || !strings.EqualFold(fmt.Sprintf("%x", digest.Sum(nil)), download.SHA1) {
		return fmt.Errorf("downloaded server jar does not match Mojang's size or checksum")
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close downloaded jar: %w", err)
	}
	if err := os.Rename(file.Name(), destination); err != nil {
		return fmt.Errorf("finalize server jar: %w", err)
	}
	return nil
}

func matchesDownload(path string, download domain.ServerDownload) (bool, error) {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() {
		return false, fmt.Errorf("%s is not a regular file", path)
	}
	if info.Size() != download.Size {
		return false, nil
	}
	digest := sha1.New()
	if _, err := io.Copy(digest, file); err != nil {
		return false, err
	}
	return strings.EqualFold(fmt.Sprintf("%x", digest.Sum(nil)), download.SHA1), nil
}
