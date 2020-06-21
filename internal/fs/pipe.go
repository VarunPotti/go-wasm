package fs

import (
	"io"
	"os"
	"time"

	"github.com/johnstarich/go-wasm/internal/interop"
)

func (f *FileDescriptors) Pipe() [2]FID {
	r, w := newPipe(f.newFID)
	f.addFileDescriptor(r)
	f.addFileDescriptor(w)
	r.Open(f.parentPID)
	w.Open(f.parentPID)
	return [2]FID{r.id, w.id}
}

func newPipe(newFID func() FID) (r, w *fileDescriptor) {
	readerFID, writerFID := newFID(), newFID()
	pipeC := newPipeChan(readerFID, writerFID)
	r = newIrregularFileDescriptor(
		readerFID,
		&pipeReadOnly{&namedPipe{pipeChan: pipeC, fid: readerFID}},
		os.ModeNamedPipe,
	)
	w = newIrregularFileDescriptor(
		writerFID,
		&pipeWriteOnly{&namedPipe{pipeChan: pipeC, fid: writerFID}},
		os.ModeNamedPipe,
	)
	return
}

type pipeChan struct {
	unimplementedFile

	buf            chan byte
	done           chan struct{}
	reader, writer FID
}

func newPipeChan(reader, writer FID) *pipeChan {
	const maxPipeBuffer = 32 << 10 // 32KiB
	return &pipeChan{
		buf:    make(chan byte, maxPipeBuffer),
		done:   make(chan struct{}),
		reader: reader,
		writer: writer,
	}
}

type pipeStat struct {
	name string
	size int64
	mode os.FileMode
}

func (p pipeStat) Name() string       { return p.name }
func (p pipeStat) Size() int64        { return p.size }
func (p pipeStat) Mode() os.FileMode  { return p.mode }
func (p pipeStat) ModTime() time.Time { return time.Time{} }
func (p pipeStat) IsDir() bool        { return false }
func (p pipeStat) Sys() interface{}   { return nil }

func (p *pipeChan) Stat() (os.FileInfo, error) {
	return &pipeStat{
		name: p.Name(),
		size: int64(len(p.buf)),
		mode: os.ModeNamedPipe,
	}, nil
}

func (p *pipeChan) Read(buf []byte) (n int, err error) {
	for n < len(buf) {
		select {
		case <-p.done:
			err = io.EOF
			return
		case b, ok := <-p.buf:
			if !ok {
				err = io.EOF
				return
			}
			buf[n] = b
			n++
			// default case is always hit immediately. this should be a blocking read
		}
	}
	if n == 0 {
		err = io.EOF
	}
	return
}

func (p *pipeChan) Write(buf []byte) (n int, err error) {
	for _, b := range buf {
		select {
		case <-p.done:
			return 0, interop.BadFileNumber(p.writer)
		case p.buf <- b:
			n++
		default:
			if n < len(buf) {
				err = io.ErrShortWrite
			}
			return
		}
	}
	if n < len(buf) {
		err = io.ErrShortWrite
	}
	return
}

func (p *pipeChan) Close() error {
	select {
	case <-p.done:
		return interop.BadFileNumber(p.writer)
	default:
		close(p.done)
		close(p.buf)
		return nil
	}
}

type namedPipe struct {
	*pipeChan
	fid FID
}

func (n *namedPipe) Name() string {
	return "pipe" + n.fid.String()
}

type pipeReadOnly struct {
	*namedPipe
}

func (r *pipeReadOnly) ReadAt(buf []byte, off int64) (n int, err error) {
	if off == 0 {
		return r.Read(buf)
	}
	return 0, interop.ErrNotImplemented
}

func (r *pipeReadOnly) Write(buf []byte) (n int, err error) {
	return 0, interop.ErrNotImplemented
}

func (r *pipeReadOnly) Close() error {
	// only write side of pipe should close the buffer
	return nil
}

type pipeWriteOnly struct {
	*namedPipe
}

func (w *pipeWriteOnly) Read(buf []byte) (n int, err error) {
	return 0, interop.ErrNotImplemented
}

func (w *pipeWriteOnly) WriteAt(buf []byte, off int64) (n int, err error) {
	if off == 0 {
		return w.Write(buf)
	}
	return 0, interop.ErrNotImplemented
}
