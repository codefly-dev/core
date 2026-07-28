package releaseupdate

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"

	"github.com/ulikunitz/xz"
)

func validateArchive(ctx context.Context, data []byte, name string, maxExpandedBytes int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	switch {
	case strings.HasSuffix(name, ".zip"):
		return validateZip(ctx, data, maxExpandedBytes)
	case strings.HasSuffix(name, ".tar.gz"), strings.HasSuffix(name, ".tgz"):
		reader, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			return fmt.Errorf("open gzip archive: %w", err)
		}
		defer reader.Close()
		if reader.Name != "" && !safeArchivePath(reader.Name) {
			return fmt.Errorf("%w: gzip header", ErrUnsafeArchive)
		}
		return validateTar(ctx, reader, maxExpandedBytes)
	case strings.HasSuffix(name, ".tar.xz"):
		dictionaryCap := maxExpandedBytes
		if dictionaryCap > int64(^uint(0)>>1) {
			dictionaryCap = int64(^uint(0) >> 1)
		}
		reader, err := (xz.ReaderConfig{DictCap: int(dictionaryCap)}).NewReader(bytes.NewReader(data))
		if err != nil {
			return fmt.Errorf("open xz archive: %w", err)
		}
		return validateTar(ctx, reader, maxExpandedBytes)
	case strings.HasSuffix(name, ".gzip"), strings.HasSuffix(name, ".gz"):
		reader, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			return fmt.Errorf("open gzip artifact: %w", err)
		}
		defer reader.Close()
		if reader.Name != "" && !safeArchivePath(reader.Name) {
			return fmt.Errorf("%w: gzip header", ErrUnsafeArchive)
		}
	case strings.HasSuffix(name, ".xz"):
		dictionaryCap := maxExpandedBytes
		if dictionaryCap > int64(^uint(0)>>1) {
			dictionaryCap = int64(^uint(0) >> 1)
		}
		reader, err := (xz.ReaderConfig{DictCap: int(dictionaryCap)}).NewReader(bytes.NewReader(data))
		if err != nil {
			return fmt.Errorf("open xz artifact: %w", err)
		}
		written, err := io.Copy(io.Discard, io.LimitReader(&contextReader{ctx: ctx, reader: reader}, maxExpandedBytes+1))
		if err != nil {
			return fmt.Errorf("read xz artifact: %w", err)
		}
		if written > maxExpandedBytes {
			return ErrDownloadLimit
		}
	}
	return nil
}

func validateZip(ctx context.Context, data []byte, maxExpandedBytes int64) error {
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return fmt.Errorf("open zip archive: %w", err)
	}
	var expanded uint64
	for _, file := range reader.File {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !safeArchivePath(file.Name) {
			return fmt.Errorf("%w: zip entry", ErrUnsafeArchive)
		}
		mode := file.Mode()
		if mode&os.ModeType != 0 && !mode.IsDir() {
			return errors.New("zip archive contains a non-regular entry")
		}
		if file.UncompressedSize64 > uint64(maxExpandedBytes) ||
			expanded > uint64(maxExpandedBytes)-file.UncompressedSize64 {
			return ErrDownloadLimit
		}
		expanded += file.UncompressedSize64
	}
	return nil
}

func validateTar(ctx context.Context, source io.Reader, maxExpandedBytes int64) error {
	counted := &countingReader{reader: io.LimitReader(&contextReader{ctx: ctx, reader: source}, maxExpandedBytes+1)}
	reader := tar.NewReader(counted)
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			if counted.count > maxExpandedBytes {
				return ErrDownloadLimit
			}
			return fmt.Errorf("read tar archive: %w", err)
		}
		if !safeArchivePath(header.Name) || (header.Linkname != "" && !safeArchivePath(header.Linkname)) {
			return fmt.Errorf("%w: tar entry", ErrUnsafeArchive)
		}
		switch header.Typeflag {
		case tar.TypeReg, tar.TypeRegA, tar.TypeDir:
		default:
			return errors.New("tar archive contains a non-regular entry")
		}
		if _, err := io.Copy(io.Discard, reader); err != nil {
			if counted.count > maxExpandedBytes {
				return ErrDownloadLimit
			}
			return fmt.Errorf("read tar entry: %w", err)
		}
	}
	if counted.count > maxExpandedBytes {
		return ErrDownloadLimit
	}
	return nil
}

func safeArchivePath(name string) bool {
	if name == "" || strings.ContainsAny(name, "\\\x00") || path.IsAbs(name) {
		return false
	}
	if len(name) >= 2 && name[1] == ':' {
		return false
	}
	cleaned := path.Clean(name)
	return cleaned != ".." && !strings.HasPrefix(cleaned, "../")
}

type countingReader struct {
	reader io.Reader
	count  int64
}

func (r *countingReader) Read(data []byte) (int, error) {
	count, err := r.reader.Read(data)
	r.count += int64(count)
	return count, err
}
