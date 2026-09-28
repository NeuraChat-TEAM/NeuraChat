package h2sidecar

import (
	"embed"
	"errors"
	"os"
	"path/filepath"
)

//go:embed neura-h2.exe
var files embed.FS

// Ensure extracts the bundled Node HTTP/2 transport beside other private tools.
func Ensure(dir string) (string, error) {
	if dir == "" {
		return "", errors.New("h2 sidecar: empty tools directory")
	}
	target := filepath.Join(dir, "neura-h2.exe")
	data, err := files.ReadFile("neura-h2.exe")
	if err != nil {
		return "", err
	}
	if info, statErr := os.Stat(target); statErr == nil && info.Size() == int64(len(data)) {
		return target, nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	tmp := target + ".tmp"
	if err := os.WriteFile(tmp, data, 0o700); err != nil {
		return "", err
	}
	_ = os.Remove(target)
	if err := os.Rename(tmp, target); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	return target, nil
}
