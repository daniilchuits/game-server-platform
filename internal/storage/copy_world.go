package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

func copyTree(ctx context.Context, source, destination string) error {
	return filepath.WalkDir(source, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if isLink(info) {
			return fmt.Errorf("symbolic links or junctions are not allowed in world: %s", path)
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, relative)
		if info.IsDir() {
			return os.Mkdir(target, 0755)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("unsupported world entry: %s", path)
		}
		return copyFile(ctx, path, target)
	})
}

func copyFile(ctx context.Context, source, destination string) (result error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer func() { result = errors.Join(result, in.Close()) }()
	out, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	// Wrapping the reader prevents io.Copy from using an OS shortcut that
	// cannot observe cancellation while copying a large region file.
	_, copyErr := io.Copy(out, contextReader{ctx: ctx, reader: in})
	return errors.Join(copyErr, out.Close(), ctx.Err())
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (reader contextReader) Read(data []byte) (int, error) {
	if err := reader.ctx.Err(); err != nil {
		return 0, err
	}
	return reader.reader.Read(data)
}
