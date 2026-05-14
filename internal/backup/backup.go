package backup

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func Write(w io.Writer, dataDir string) error {
	zw := zip.NewWriter(w)
	defer zw.Close()
	return filepath.WalkDir(dataDir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		name, _ := filepath.Rel(".", p)
		f, err := zw.Create(name)
		if err != nil {
			return err
		}
		src, err := os.Open(p)
		if err != nil {
			return nil
		}
		defer src.Close()
		_, err = io.Copy(f, src)
		return err
	})
}

func Restore(zipPath, dataDir string) error {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer zr.Close()

	tempDir, err := os.MkdirTemp(filepath.Dir(dataDir), "speedrss-restore-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tempDir)

	prefix := dataDir + string(os.PathSeparator)
	for _, file := range zr.File {
		clean := filepath.Clean(file.Name)
		if clean == "." || strings.HasPrefix(clean, "..") || filepath.IsAbs(clean) {
			return fmt.Errorf("unsafe backup path: %s", file.Name)
		}
		if clean == dataDir {
			continue
		}
		if !strings.HasPrefix(clean, prefix) {
			return fmt.Errorf("backup must contain files under %s/", dataDir)
		}
		target := filepath.Join(tempDir, strings.TrimPrefix(clean, prefix))
		if file.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return err
		}
		src, err := file.Open()
		if err != nil {
			return err
		}
		dst, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, file.Mode())
		if err != nil {
			_ = src.Close()
			return err
		}
		_, copyErr := io.Copy(dst, src)
		closeErr := dst.Close()
		_ = src.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	if _, err := os.Stat(filepath.Join(tempDir, "speedrss.db")); err != nil {
		return errors.New("backup does not contain data/speedrss.db")
	}
	oldDir := dataDir + ".old-" + time.Now().Format("20060102150405")
	if _, err := os.Stat(dataDir); err == nil {
		if err := os.Rename(dataDir, oldDir); err != nil {
			return err
		}
	}
	if err := os.Rename(tempDir, dataDir); err != nil {
		if _, statErr := os.Stat(oldDir); statErr == nil {
			_ = os.Rename(oldDir, dataDir)
		}
		return err
	}
	return nil
}
