package namespace

import (
	"fmt"
	"io"
	"os"
)

func (n *NamespaceNode) CopyFrom(srcPath, destPath string) error {
	return copyFile(srcPath, destPath)
}

func (n *NamespaceNode) CopyTo(srcPath, destPath string) error {
	return copyFile(srcPath, destPath)
}

func copyFile(srcPath, destPath string) error {
	info, err := os.Stat(srcPath)
	if err != nil {
		return fmt.Errorf("unable to stat %s: %w", srcPath, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", srcPath)
	}

	src, err := os.Open(srcPath)
	if err != nil {
		return fmt.Errorf("unable to open %s: %w", srcPath, err)
	}
	defer src.Close()

	dest, err := os.OpenFile(destPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return fmt.Errorf("unable to create %s: %w", destPath, err)
	}

	_, copyErr := io.Copy(dest, src)
	closeErr := dest.Close()
	if copyErr != nil {
		return fmt.Errorf("unable to copy %s to %s: %w", srcPath, destPath, copyErr)
	}
	if closeErr != nil {
		return fmt.Errorf("unable to close %s: %w", destPath, closeErr)
	}
	return nil
}
