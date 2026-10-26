package utils

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func AddFileToTar(tw *tar.Writer, path, dstPath string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()

	stat, err := file.Stat()
	if err != nil {
		return err
	}

	// now lets create the header as needed for this file within the tarball
	header := new(tar.Header)
	header.Name = dstPath
	header.Size = stat.Size()
	header.Mode = int64(stat.Mode())
	header.ModTime = stat.ModTime()
	// write the header to the tarball archive
	if err := tw.WriteHeader(header); err != nil {
		return err
	}
	// copy the file data to the tarball
	if _, err := io.Copy(tw, file); err != nil {
		return err
	}

	return nil
}

func CreateOneFileArchive(w io.Writer, filename string, data []byte) error {
	gw := gzip.NewWriter(w)
	tw := tar.NewWriter(gw)

	header := new(tar.Header)
	header.Name = filename
	header.Size = int64(len(data))
	header.Mode = int64(0644)
	header.ModTime = time.Now()

	if err := tw.WriteHeader(header); err != nil {
		_ = tw.Close()
		_ = gw.Close()
		return err
	}
	if _, err := io.Copy(tw, bytes.NewReader(data)); err != nil {
		_ = tw.Close()
		_ = gw.Close()
		return err
	}

	if err := tw.Close(); err != nil {
		_ = gw.Close()
		return err
	}
	return gw.Close()
}

func CreateArchive(sourcePath string, w io.Writer) error {
	gw := gzip.NewWriter(w)
	tw := tar.NewWriter(gw)

	walkErr := filepath.Walk(sourcePath, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		relPath, err := filepath.Rel(sourcePath, path)
		if err != nil {
			return err
		}
		if info.IsDir() {
			header, err := tar.FileInfoHeader(info, "")
			if err != nil {
				return err
			}
			header.Name = relPath

			return tw.WriteHeader(header)
		}
		return AddFileToTar(tw, path, relPath)
	})

	if walkErr != nil {
		_ = tw.Close()
		_ = gw.Close()
		return walkErr
	}

	if err := tw.Close(); err != nil {
		_ = gw.Close()
		return err
	}
	return gw.Close()
}

func safeArchivePath(root, name string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("empty archive entry name")
	}
	if filepath.IsAbs(name) {
		return "", fmt.Errorf("absolute archive path %q is not allowed", name)
	}

	target := filepath.Join(root, name)
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return "", fmt.Errorf("unable to resolve archive path %q: %w", name, err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("archive entry %q escapes destination", name)
	}

	return target, nil
}

func OpenArchive(dstPath string, r io.Reader) error {
	// check the format of data (tar.gz)
	gzf, err := gzip.NewReader(r)
	if err != nil {
		return err
	}
	defer gzf.Close()

	// extract project in the destination folder
	tarReader := tar.NewReader(gzf)
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("archive is not a valid tar: %w", err)
		}

		target, err := safeArchivePath(dstPath, header.Name)
		if err != nil {
			return err
		}

		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0755); err != nil {
				return fmt.Errorf("unable to create folder %q: %w", target, err)
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return fmt.Errorf("unable to create parent folder for %q: %w", target, err)
			}

			outFile, err := os.OpenFile(
				target,
				os.O_CREATE|os.O_WRONLY|os.O_TRUNC,
				os.FileMode(header.Mode)&0777,
			)
			if err != nil {
				return fmt.Errorf("unable to create file %q: %w", target, err)
			}

			_, copyErr := io.Copy(outFile, tarReader)
			closeErr := outFile.Close()
			if copyErr != nil {
				return fmt.Errorf("unable to copy file %q: %w", target, copyErr)
			}
			if closeErr != nil {
				return fmt.Errorf("unable to close file %q: %w", target, closeErr)
			}
		default:
			return fmt.Errorf(
				"unsupported archive entry type %q for %q",
				header.Typeflag, header.Name,
			)
		}
	}

	return nil
}
