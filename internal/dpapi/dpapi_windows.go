//go:build windows

package dpapi

import (
	"errors"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Windows DPAPI wrappers. The session cookie never touches the disk in plain text.

const cryptprotectUIForbidden = 0x1

type dataBlob struct {
	cbData uint32
	pbData *byte
}

func newBlob(d []byte) dataBlob {
	if len(d) == 0 {
		return dataBlob{}
	}
	return dataBlob{cbData: uint32(len(d)), pbData: &d[0]}
}

func (b dataBlob) bytes() []byte {
	if b.cbData == 0 || b.pbData == nil {
		return nil
	}
	out := make([]byte, b.cbData)
	copy(out, unsafe.Slice(b.pbData, b.cbData))
	return out
}

var (
	crypt32          = windows.NewLazySystemDLL("crypt32.dll")
	kernel32         = windows.NewLazySystemDLL("kernel32.dll")
	procProtectData  = crypt32.NewProc("CryptProtectData")
	procUnprotect    = crypt32.NewProc("CryptUnprotectData")
	procLocalFreeMem = kernel32.NewProc("LocalFree")
)

// Protect encrypts plain bytes for the current Windows user.
func Protect(plain []byte) ([]byte, error) {
	if len(plain) == 0 {
		return nil, errors.New("dpapi: empty payload")
	}
	in := newBlob(plain)
	var out dataBlob
	r, _, err := procProtectData.Call(
		uintptr(unsafe.Pointer(&in)), 0, 0, 0, 0,
		uintptr(cryptprotectUIForbidden),
		uintptr(unsafe.Pointer(&out)),
	)
	if r == 0 {
		return nil, err
	}
	defer procLocalFreeMem.Call(uintptr(unsafe.Pointer(out.pbData)))
	return out.bytes(), nil
}

// Unprotect decrypts bytes produced by Protect on the same machine and user.
func Unprotect(cipher []byte) ([]byte, error) {
	if len(cipher) == 0 {
		return nil, errors.New("dpapi: empty payload")
	}
	in := newBlob(cipher)
	var out dataBlob
	r, _, err := procUnprotect.Call(
		uintptr(unsafe.Pointer(&in)), 0, 0, 0, 0,
		uintptr(cryptprotectUIForbidden),
		uintptr(unsafe.Pointer(&out)),
	)
	if r == 0 {
		return nil, err
	}
	defer procLocalFreeMem.Call(uintptr(unsafe.Pointer(out.pbData)))
	return out.bytes(), nil
}
