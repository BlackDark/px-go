//go:build windows

package clientauth

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
	secpkgCredInbound  = 1
	ascReqConnection   = 0x00000800
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

type sspiServer struct {
	cred    secHandle
	ctx     secHandle
	started bool
}

var (
	secur32                       = windows.NewLazySystemDLL("secur32.dll")
	procAcquireCredentialsHandleW = secur32.NewProc("AcquireCredentialsHandleW")
	procAcceptSecurityContext     = secur32.NewProc("AcceptSecurityContext")
	procFreeCredentialsHandle     = secur32.NewProc("FreeCredentialsHandle")
	procDeleteSecurityContext     = secur32.NewProc("DeleteSecurityContext")
)

func newNegotiateServer() (authServer, error) {
	server := &sspiServer{}
	packageName, err := windows.UTF16PtrFromString("Negotiate")
	if err != nil {
		return nil, err
	}
	var expiry timeStamp
	ret, _, _ := procAcquireCredentialsHandleW.Call(
		0,
		uintptr(unsafe.Pointer(packageName)),
		uintptr(secpkgCredInbound),
		0,
		0,
		0,
		0,
		uintptr(unsafe.Pointer(&server.cred)),
		uintptr(unsafe.Pointer(&expiry)),
	)
	if uint32(ret) != secEOk {
		return nil, fmt.Errorf("AcquireCredentialsHandleW failed: 0x%x", ret)
	}
	return server, nil
}

func (s *sspiServer) Accept(in []byte) ([]byte, string, bool, error) {
	inSec := secBuffer{cbBuffer: uint32(len(in)), bufferType: secbufferToken, pvBuffer: uintptr(unsafe.Pointer(&in[0]))}
	inDesc := secBufferDesc{ulVersion: secbufferVersion, cBuffers: 1, pBuffers: &inSec}
	outBuf := make([]byte, maxTokenBuffer)
	outSec := secBuffer{cbBuffer: uint32(len(outBuf)), bufferType: secbufferToken, pvBuffer: uintptr(unsafe.Pointer(&outBuf[0]))}
	outDesc := secBufferDesc{ulVersion: secbufferVersion, cBuffers: 1, pBuffers: &outSec}
	var newCtx secHandle
	var attrs uint32
	var expiry timeStamp
	ret, _, _ := procAcceptSecurityContext.Call(
		uintptr(unsafe.Pointer(&s.cred)),
		func() uintptr {
			if s.started {
				return uintptr(unsafe.Pointer(&s.ctx))
			}
			return 0
		}(),
		uintptr(unsafe.Pointer(&inDesc)),
		uintptr(ascReqConnection),
		uintptr(securityNativeDrep),
		uintptr(unsafe.Pointer(&newCtx)),
		uintptr(unsafe.Pointer(&outDesc)),
		uintptr(unsafe.Pointer(&attrs)),
		uintptr(unsafe.Pointer(&expiry)),
	)
	s.ctx = newCtx
	s.started = true
	switch uint32(ret) {
	case secEOk:
		return outBuf[:outSec.cbBuffer], "", true, nil
	case secIContinueNeeded:
		return outBuf[:outSec.cbBuffer], "", false, nil
	default:
		return nil, "", false, fmt.Errorf("AcceptSecurityContext failed: 0x%x", ret)
	}
}

func (s *sspiServer) Close() error {
	if s.started {
		_, _, _ = procDeleteSecurityContext.Call(uintptr(unsafe.Pointer(&s.ctx)))
	}
	_, _, _ = procFreeCredentialsHandle.Call(uintptr(unsafe.Pointer(&s.cred)))
	return nil
}
