// Package oplog keeps the output of long-running operations (builds) in a
// bounded form: a fixed-size in-memory ring while the operation runs, then the
// retained tail in a file of at most the same size. Readers use absolute byte
// offsets, so a client that polls with the last offset it saw receives only
// new output, and learns when earlier output was dropped.
package oplog

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sync"

	"github.com/xenycx/rivetpanel/internal/lazyre"
)

// DefaultMax is the retained output per operation.
const DefaultMax = 256 << 10

var idRe = lazyre.New(`^[0-9a-f-]{36}$`)

// ErrUnknown means there is no output for the operation.
var ErrUnknown = errors.New("no output for this operation")

// Store owns the live buffers and the directory of retained logs.
type Store struct {
	dir string
	max int

	mu   sync.Mutex
	live map[string]*Writer
}

// New creates the directory (mode 0700) if needed.
func New(dir string, max int) (*Store, error) {
	if max <= 0 {
		max = DefaultMax
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return &Store{dir: dir, max: max, live: map[string]*Writer{}}, nil
}

func (s *Store) path(id string) (string, error) {
	if !idRe.MatchString(id) {
		return "", fmt.Errorf("invalid operation id")
	}
	return filepath.Join(s.dir, id+".log"), nil
}

// Writer collects output for one running operation.
type Writer struct {
	s     *Store
	id    string
	mu    sync.Mutex
	buf   []byte // ring storage, len == cap once full
	start int    // index of the oldest byte when full
	full  bool
	total int64
	done  bool
}

// Open returns the live writer for an operation (creating it).
func (s *Store) Open(id string) (*Writer, error) {
	if _, err := s.path(id); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if w := s.live[id]; w != nil {
		return w, nil
	}
	w := &Writer{s: s, id: id, buf: make([]byte, 0, 16<<10)}
	s.live[id] = w
	return w, nil
}

// Write appends output; it never fails and never blocks on readers.
func (w *Writer) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n := len(p)
	w.total += int64(n)
	max := w.s.max
	if n >= max {
		w.buf = append(w.buf[:0], p[n-max:]...)
		w.start, w.full = 0, true
		return n, nil
	}
	for len(p) > 0 {
		if !w.full {
			room := max - len(w.buf)
			k := min(room, len(p))
			w.buf = append(w.buf, p[:k]...)
			p = p[k:]
			if len(w.buf) == max {
				w.full = true
				w.start = 0
			}
			continue
		}
		k := copy(w.buf[w.start:], p)
		p = p[k:]
		w.start = (w.start + k) % max
	}
	return n, nil
}

// snapshot returns the retained bytes in order and the total written.
func (w *Writer) snapshot() ([]byte, int64) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.full {
		return append([]byte(nil), w.buf...), w.total
	}
	out := make([]byte, 0, len(w.buf))
	out = append(out, w.buf[w.start:]...)
	out = append(out, w.buf[:w.start]...)
	return out, w.total
}

// Total is the number of bytes written so far.
func (w *Writer) Total() int64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.total
}

// Close persists the retained tail and ends live reading. The file starts
// with the absolute offset of its first byte so reads stay consistent.
func (w *Writer) Close() error {
	data, total := w.snapshot()
	p, _ := w.s.path(w.id)
	err := writeFile(p, data, total)
	w.s.mu.Lock()
	delete(w.s.live, w.id)
	w.s.mu.Unlock()
	return err
}

func writeFile(p string, data []byte, total int64) error {
	tmp := p + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(f, "%020d\n", total-int64(len(data))); err == nil {
		_, err = f.Write(data)
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, p)
}

// Chunk is the result of a read.
type Chunk struct {
	Data      []byte
	Next      int64 // offset to request next
	Total     int64 // bytes produced so far
	Truncated bool  // output between the requested offset and Data was dropped
	Live      bool  // the operation is still producing output
}

// Read returns up to limit bytes starting at offset.
func (s *Store) Read(id string, offset int64, limit int) (Chunk, error) {
	p, err := s.path(id)
	if err != nil {
		return Chunk{}, err
	}
	if limit <= 0 || limit > s.max {
		limit = s.max
	}
	s.mu.Lock()
	w := s.live[id]
	s.mu.Unlock()
	var data []byte
	var total int64
	live := w != nil
	if live {
		data, total = w.snapshot()
	} else {
		f, err := os.Open(p)
		if errors.Is(err, fs.ErrNotExist) {
			return Chunk{}, ErrUnknown
		}
		if err != nil {
			return Chunk{}, err
		}
		defer f.Close()
		var first int64
		if _, err := fmt.Fscanf(f, "%020d\n", &first); err != nil {
			return Chunk{}, fmt.Errorf("corrupt log header: %w", err)
		}
		if data, err = io.ReadAll(io.LimitReader(f, int64(s.max)+1)); err != nil {
			return Chunk{}, err
		}
		total = first + int64(len(data))
	}
	first := total - int64(len(data))
	c := Chunk{Total: total, Live: live}
	if offset < first {
		c.Truncated = offset < first && offset >= 0
		offset = first
	}
	if offset > total {
		offset = total
	}
	rel := offset - first
	end := min(rel+int64(limit), int64(len(data)))
	c.Data = data[rel:end]
	c.Next = first + end
	return c, nil
}

// Remove deletes a retained log (missing is fine).
func (s *Store) Remove(id string) {
	if p, err := s.path(id); err == nil {
		_ = os.Remove(p)
	}
}

// Sweep deletes retained logs whose operation no longer exists.
func (s *Store) Sweep(keep map[string]bool) {
	ents, err := os.ReadDir(s.dir)
	if err != nil {
		return
	}
	for _, e := range ents {
		name := e.Name()
		id := name[:max(0, len(name)-len(".log"))]
		if filepath.Ext(name) == ".log" && idRe.MatchString(id) && !keep[id] {
			_ = os.Remove(filepath.Join(s.dir, name))
		}
	}
}
