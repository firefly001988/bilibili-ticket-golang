package utils

import (
	"os"
	"path/filepath"
	"strings"
)

// IsFileEmpty checks whether the given file exists and is empty.
func IsFileEmpty(path string) bool {
	fileInfo, err := os.Stat(path)
	if err != nil {
		return false
	}
	return fileInfo.Size() == 0
}

// GetFileNameWithoutExt returns the file name without its extension.
func GetFileNameWithoutExt(path string) string {
	filename := filepath.Base(path)
	return strings.TrimSuffix(filename, filepath.Ext(filename))
}
