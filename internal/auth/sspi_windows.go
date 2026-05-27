//go:build windows

package auth

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	secEOk             = 0x00000000
	secIContinueNeeded = 0x00090312
	secbufferVersion   = 0
	secbufferToken     = 2
	secpkgCredOutbound = 2
	iscReqConnection   = 0x00000800
	securityNativeDrep = 0x00000010
	maxTokenBuffer     = 12288
)

type secHandle struct {
	lower uintptr
	upper uintptr
}

type timeStamp struct {
	lowPart  uint32
	highPart uint32
}

type secBuffer struct {
	cbBuffer   uint32
	bufferType uint32
	pvBuffer   uintptr
}

type secBufferDesc struct {
	ulVersion uint32
	cBuffers  uint32
	pBuffers  *secBuffer
}

type secWinntAuthIdentity struct {
	user           *uint16
	userLength     uint32
	domain         *uint16
	domainLength   uint32
	password       *uint16
	passwordLength uint32
	flags          uint32
}

type sspiClient struct {
	cred    secHandle
	ctx     secHandle
	started bool
}

var (
	secur32                       = windows.NewLazySystemDLL("secur32.dll")
	procAcquireCredentialsHandleW = secur32.NewProc("AcquireCredentialsHandleW")
	procInitializeSecurityContext = secur32.NewProc("InitializeSecurityContextW")
	procFreeCredentialsHandle     = secur32.NewProc("FreeCredentialsHandle")
	procDeleteSecurityContext     = secur32.NewProc("DeleteSecurityContext")
)

type tokenClient interface {
	Next(in []byte) ([]byte, bool, error)
	Close() error
}

type tokenServer interface {
	Accept(in []byte) (out []byte, username string, done bool, err error)
	Close() error
}

func newTokenClient(pkg string, creds Credentials) (tokenClient, error) {
	client := &sspiClient{}
	packageName, err := windows.UTF16PtrFromString(pkg)
	if err != nil {
		return nil, err
	}
	var identity *secWinntAuthIdentity
	var authBuf secWinntAuthIdentity
	var userPtr, domainPtr, passPtr *uint16
	if creds.Username != "" {
		user, domain, _ := parseUser(creds.Username)
		if user != "" {
			userPtr, _ = windows.UTF16PtrFromString(user)
			passPtr, _ = windows.UTF16PtrFromString(creds.Password)
			authBuf.user = userPtr
			authBuf.userLength = uint32(len(user))
			authBuf.password = passPtr
			authBuf.passwordLength = uint32(len(creds.Password))
			if domain != "" {
				domainPtr, _ = windows.UTF16PtrFromString(domain)
				authBuf.domain = domainPtr
				authBuf.domainLength = uint32(len(domain))
			}
			authBuf.flags = 2
			identity = &authBuf
		}
	}
	var expiry timeStamp
	ret, _, _ := procAcquireCredentialsHandleW.Call(
		0,
		uintptr(unsafe.Pointer(packageName)),
		uintptr(secpkgCredOutbound),
		0,
		uintptr(unsafe.Pointer(identity)),
		0,
		0,
		uintptr(unsafe.Pointer(&client.cred)),
		uintptr(unsafe.Pointer(&expiry)),
	)
	if uint32(ret) != secEOk {
		return nil, fmt.Errorf("AcquireCredentialsHandleW failed: 0x%x", ret)
	}
	return client, nil
}

func (c *sspiClient) Next(in []byte) ([]byte, bool, error) {
	outBuf := make([]byte, maxTokenBuffer)
	outSec := secBuffer{cbBuffer: uint32(len(outBuf)), bufferType: secbufferToken, pvBuffer: uintptr(unsafe.Pointer(&outBuf[0]))}
	outDesc := secBufferDesc{ulVersion: secbufferVersion, cBuffers: 1, pBuffers: &outSec}
	var inDesc *secBufferDesc
	var inSec secBuffer
	if len(in) > 0 {
		inSec = secBuffer{cbBuffer: uint32(len(in)), bufferType: secbufferToken, pvBuffer: uintptr(unsafe.Pointer(&in[0]))}
		inDesc = &secBufferDesc{ulVersion: secbufferVersion, cBuffers: 1, pBuffers: &inSec}
	}
	var newCtx secHandle
	var attrs uint32
	var expiry timeStamp
	ret, _, _ := procInitializeSecurityContext.Call(
		uintptr(unsafe.Pointer(&c.cred)),
		func() uintptr {
			if c.started {
				return uintptr(unsafe.Pointer(&c.ctx))
			}
			return 0
		}(),
		0,
		uintptr(iscReqConnection),
		0,
		uintptr(securityNativeDrep),
		uintptr(unsafe.Pointer(inDesc)),
		0,
		uintptr(unsafe.Pointer(&newCtx)),
		uintptr(unsafe.Pointer(&outDesc)),
		uintptr(unsafe.Pointer(&attrs)),
		uintptr(unsafe.Pointer(&expiry)),
	)
	c.ctx = newCtx
	c.started = true
	switch uint32(ret) {
	case secEOk:
		return outBuf[:outSec.cbBuffer], true, nil
	case secIContinueNeeded:
		return outBuf[:outSec.cbBuffer], false, nil
	default:
		return nil, false, fmt.Errorf("InitializeSecurityContextW failed: 0x%x", ret)
	}
}

func (c *sspiClient) Close() error {
	if c.started {
		_, _, _ = procDeleteSecurityContext.Call(uintptr(unsafe.Pointer(&c.ctx)))
	}
	_, _, _ = procFreeCredentialsHandle.Call(uintptr(unsafe.Pointer(&c.cred)))
	return nil
}
