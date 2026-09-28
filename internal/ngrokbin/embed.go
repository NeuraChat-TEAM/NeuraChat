package ngrokbin

import (
	"embed"
	"errors"
	"os"
	"path/filepath"
)

//go:embed ngrok.exe
var files embed.FS

// Ensure extracts the bundled Windows binary into the private application tools directory.
func Ensure(dir string) (string, error) {
	if dir == "" {
		return "", errors.New("ngrok: empty tools directory")
	}
	target := filepath.Join(dir, "ngrok.exe")
	data, err := files.ReadFile("ngrok.exe")
	if err != nil {
		return "", err
	}
	if info, err := os.Stat(target); err == nil && info.Size() == int64(len(data)) {
		return target, nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	tmp := target + ".tmp"
	if err := os.WriteFile(tmp, data, 0o700); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, target); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	return target, nil
}
