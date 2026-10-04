package blueprint

import (
	"bufio"
	"compress/flate"
	"encoding/binary"
	"errors"
	"io"
	"strings"
	"sync"
)

// JarJavaSniffer is an io.Writer that reads a jar (ZIP) stream as it is
// written and records the highest class-file version among its classes, so a
// download can be checked against the Java runtime that will start it. It
// never fails a write: an unreadable or unusual archive simply yields 0.
//
// Entries under META-INF/ are ignored: multi-release jars keep classes for
// newer Java versions there that are only loaded when that Java is present.
type JarJavaSniffer struct {
	pw   *io.PipeWriter
	done chan struct{}
	once sync.Once
	java int
}

// NewJarJavaSniffer starts a sniffer; call Java after the last write.
func NewJarJavaSniffer() *JarJavaSniffer {
	pr, pw := io.Pipe()
	s := &JarJavaSniffer{pw: pw, done: make(chan struct{})}
	go func() {
		defer close(s.done)
		s.java = jarJava(bufio.NewReaderSize(pr, 64<<10))
		_, _ = io.Copy(io.Discard, pr) // keep accepting writes after the scan stops
	}()
	return s
}

func (s *JarJavaSniffer) Write(p []byte) (int, error) {
	_, _ = s.pw.Write(p)
	return len(p), nil
}

// Java finishes the scan and returns the Java major version the classes need
// (class-file major minus 44), or 0 when it could not be determined.
func (s *JarJavaSniffer) Java() int {
	s.once.Do(func() { _ = s.pw.Close() })
	<-s.done
	return s.java
}

// ClassFileJava maps a class-file major version to a Java major version
// (52 → 8, 65 → 21, 69 → 25), or 0 for an implausible value.
func ClassFileJava(major int) int {
	if major < 45 || major > 44+99 {
		return 0
	}
	if major < 49 {
		return 1 // Java 1.1–1.4; any runtime will do
	}
	return major - 44
}

const (
	zipLocalSig   = 0x04034b50
	zipDescSig    = 0x08074b50
	maxJarEntries = 200000
)

var errStopScan = errors.New("stop")

// jarJava walks the local file headers of a ZIP stream. Deflated entries
// with a trailing data descriptor are inflated to find their end; others are
// skipped by their declared size. Only the first 8 bytes of a class are read.
func jarJava(r *bufio.Reader) int {
	best := 0
	// One inflater and one entry buffer serve every entry: a fresh
	// flate.Reader costs about 40 KiB, and a server jar has thousands of
	// entries (allocating one per entry churned through about 1 GB of heap
	// for a single install).
	var inflater io.ReadCloser
	inflate := func(src io.Reader) io.Reader {
		if inflater == nil {
			inflater = flate.NewReader(src)
		} else {
			_ = inflater.(flate.Resetter).Reset(src, nil)
		}
		return inflater
	}
	entry := bufio.NewReaderSize(nil, 4<<10) // keeps the inflater from wrapping each entry
	for i := 0; i < maxJarEntries; i++ {
		var h [30]byte
		if _, err := io.ReadFull(r, h[:]); err != nil {
			return best
		}
		if binary.LittleEndian.Uint32(h[0:]) != zipLocalSig {
			return best // central directory (or something unexpected): done
		}
		flags := binary.LittleEndian.Uint16(h[6:])
		method := binary.LittleEndian.Uint16(h[8:])
		csize := int64(binary.LittleEndian.Uint32(h[18:]))
		nameLen := int(binary.LittleEndian.Uint16(h[26:]))
		extraLen := int(binary.LittleEndian.Uint16(h[28:]))
		name := make([]byte, nameLen)
		if _, err := io.ReadFull(r, name); err != nil {
			return best
		}
		if _, err := r.Discard(extraLen); err != nil {
			return best
		}
		descriptor := flags&0x8 != 0
		if flags&0x1 != 0 || csize == 0xFFFFFFFF || (descriptor && method != 8) {
			return best // encrypted, ZIP64 or an unknown length: stop here
		}
		isClass := strings.HasSuffix(string(name), ".class") && !strings.HasPrefix(string(name), "META-INF/")
		var major int
		switch {
		case method == 8 && descriptor:
			fr := inflate(r) // r is an io.ByteReader: inflating stops exactly at the end
			major = readClassMajor(fr, isClass)
			if _, err := io.Copy(io.Discard, fr); err != nil {
				return best
			}
			if err := skipDescriptor(r); err != nil {
				return best
			}
		case method == 8:
			lr := io.LimitReader(r, csize)
			if isClass {
				entry.Reset(lr)
				major = readClassMajor(inflate(entry), true)
			}
			if _, err := io.Copy(io.Discard, lr); err != nil {
				return best
			}
		case method == 0:
			lr := io.LimitReader(r, csize)
			major = readClassMajor(lr, isClass)
			if _, err := io.Copy(io.Discard, lr); err != nil {
				return best
			}
		default:
			if _, err := io.CopyN(io.Discard, r, csize); err != nil {
				return best
			}
		}
		if j := ClassFileJava(major); j > best {
			best = j
		}
	}
	return best
}

// readClassMajor reads a class header (CAFEBABE, minor, major).
func readClassMajor(r io.Reader, isClass bool) int {
	if !isClass {
		return 0
	}
	var b [8]byte
	if _, err := io.ReadFull(r, b[:]); err != nil {
		return 0
	}
	if binary.BigEndian.Uint32(b[0:]) != 0xCAFEBABE {
		return 0
	}
	return int(binary.BigEndian.Uint16(b[6:]))
}

// skipDescriptor skips a data descriptor: an optional signature, CRC-32 and
// two 32-bit sizes.
func skipDescriptor(r *bufio.Reader) error {
	peek, err := r.Peek(4)
	if err != nil {
		return err
	}
	n := 12
	if binary.LittleEndian.Uint32(peek) == zipDescSig {
		n = 16
	}
	_, err = r.Discard(n)
	if err != nil {
		return errStopScan
	}
	return nil
}
