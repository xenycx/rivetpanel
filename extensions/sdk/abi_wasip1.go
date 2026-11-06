//go:build wasip1

package sdk

import "unsafe"

//go:wasmimport rivet host_call
func rawHostCall(name unsafe.Pointer, nameLen uint32, req unsafe.Pointer, reqLen uint32) uint32

//go:wasmimport rivet host_result
func rawHostResult(dst unsafe.Pointer)

func ptr(b []byte) unsafe.Pointer {
	if len(b) == 0 {
		return nil
	}
	return unsafe.Pointer(&b[0])
}

// hostCall sends a request and copies the reply out in a second call, so the
// host never has to call back into the guest while it is suspended.
func hostCall(name string, req []byte) []byte {
	nb := []byte(name)
	n := rawHostCall(ptr(nb), uint32(len(nb)), ptr(req), uint32(len(req)))
	out := make([]byte, n)
	if n > 0 {
		rawHostResult(ptr(out))
	}
	return out
}

// pinned keeps buffers handed to the host alive until the next invocation.
var pinned [][]byte

//go:wasmexport rivet_alloc
func rivetAlloc(size uint32) unsafe.Pointer {
	b := make([]byte, size)
	pinned = append(pinned, b)
	return ptr(b)
}

//go:wasmexport rivet_handle
func rivetHandle(p unsafe.Pointer, n uint32) uint64 {
	in := unsafe.Slice((*byte)(p), n)
	out := dispatch(in)
	pinned = append(pinned[:0], out) // the input buffer is no longer needed
	if len(out) == 0 {
		return 0
	}
	return uint64(uintptr(unsafe.Pointer(&out[0])))<<32 | uint64(len(out))
}
