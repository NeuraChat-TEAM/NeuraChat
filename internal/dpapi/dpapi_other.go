//go:build !windows

package dpapi

import "errors"

// On non-Windows builds the session file is stored as-is. Wails targets Windows
// here, these stubs only keep `go vet` happy on other platforms.

func Protect(plain []byte) ([]byte, error) {
	if len(plain) == 0 {
		return nil, errors.New("dpapi: empty payload")
	}
	out := make([]byte, len(plain))
	copy(out, plain)
	return out, nil
}

func Unprotect(cipher []byte) ([]byte, error) {
	if len(cipher) == 0 {
		return nil, errors.New("dpapi: empty payload")
	}
	out := make([]byte, len(cipher))
	copy(out, cipher)
	return out, nil
}
