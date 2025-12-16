package main

import (
	"fmt"
	"os"
	"path/filepath"
)

// uniquePath возвращает путь, который не перезапишет существующий файл
func uniquePath(dir string, filename string) string {
	ext := filepath.Ext(filename)
	base := filename[:len(filename)-len(ext)]

	path := filepath.Join(dir, filename)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return path
	}

	for i := 1; ; i++ {
		name := fmt.Sprintf("%s (%d)%s", base, i, ext)
		path = filepath.Join(dir, name)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			return path
		}
	}
}
