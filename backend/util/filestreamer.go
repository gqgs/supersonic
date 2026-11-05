package util

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"sync"
	"time"
)

// FileStreamerServer exposes a growing file to a local decoder. It shuts down
// when its input fails, its owner or reader cancels, or the request completes.
type FileStreamerServer struct {
	Path       string
	IsComplete func() bool
	listener   net.Listener
	server     *http.Server
	ctx        context.Context
	cancel     context.CancelFunc
	status     func() (bool, error)
	done       chan struct{}
	doneOnce   sync.Once
	handlerMu  sync.Mutex
	handlers   sync.WaitGroup
	closed     bool
}

func NewFileStreamerServer(path string, isComplete func() bool) (*FileStreamerServer, error) {
	return NewFileStreamerServerWithContext(context.Background(), path, func() (bool, error) {
		return isComplete(), nil
	})
}

// status reports terminal completion and, if unsuccessful, the input error.
func NewFileStreamerServerWithContext(ctx context.Context, path string, status func() (bool, error)) (*FileStreamerServer, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(ctx)
	fs := &FileStreamerServer{
		Path: path, listener: listener, ctx: ctx, cancel: cancel,
		status: status, done: make(chan struct{}),
	}
	fs.IsComplete = func() bool { done, _ := status(); return done }
	fs.server = &http.Server{Handler: http.HandlerFunc(fs.streamHandler)}
	return fs, nil
}

func (fs *FileStreamerServer) Addr() string {
	return "http://" + fs.listener.Addr().String() + "/"
}

// Close also releases a listener that never received a decoder request.
func (fs *FileStreamerServer) Close() {
	fs.handlerMu.Lock()
	fs.closed = true
	fs.handlerMu.Unlock()
	fs.cancel()
	_ = fs.server.Close()
	_ = fs.listener.Close()
	// In particular on Windows, readers must close their file handles before
	// the owner releases/removes the cached input file.
	fs.handlers.Wait()
}

func (fs *FileStreamerServer) Serve() error {
	defer fs.Close()
	served := make(chan error, 1)
	go func() { served <- fs.server.Serve(fs.listener) }()
	select {
	case <-fs.done:
		// The handler signals just before returning; let net/http finish the
		// response (including the final chunk) before closing connections.
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		return fs.server.Shutdown(ctx)
	case <-fs.ctx.Done():
		return fs.ctx.Err()
	case err := <-served:
		if errors.Is(err, http.ErrServerClosed) || errors.Is(err, net.ErrClosed) {
			return nil
		}
		return err
	}
}

func (fs *FileStreamerServer) streamHandler(w http.ResponseWriter, req *http.Request) {
	fs.handlerMu.Lock()
	if fs.closed {
		fs.handlerMu.Unlock()
		http.Error(w, "stream closed", http.StatusServiceUnavailable)
		return
	}
	fs.handlers.Add(1)
	fs.handlerMu.Unlock()
	defer fs.handlers.Done()
	defer fs.doneOnce.Do(func() { close(fs.done) })
	if _, err := fs.status(); err != nil {
		http.Error(w, "audio input unavailable", http.StatusBadGateway)
		return
	}
	file, err := os.Open(fs.Path)
	if err != nil {
		http.Error(w, "could not open file", http.StatusInternalServerError)
		return
	}
	defer file.Close()
	w.Header().Set("Content-Type", "application/octet-stream")
	flusher, canFlush := w.(http.Flusher)

	bytesRead := int64(0)
	buf := make([]byte, 4096)
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		if fs.ctx.Err() != nil || req.Context().Err() != nil {
			panic(http.ErrAbortHandler)
		}
		complete, err := fs.status()
		if err != nil {
			// Do not turn failed partial input into a successful EOF.
			panic(http.ErrAbortHandler)
		}
		buf = buf[:cap(buf)]
		if !complete {
			s, err := file.Stat()
			if err != nil {
				panic(http.ErrAbortHandler)
			}
			maxToRead := max(0, s.Size()-bytesRead-1024)
			buf = buf[:min(int64(cap(buf)), maxToRead)]
		}
		n, err := file.Read(buf)
		if err != nil && err != io.EOF {
			panic(http.ErrAbortHandler)
		}
		bytesRead += int64(n)
		if n > 0 {
			if _, err := w.Write(buf[:n]); err != nil {
				return
			}
			if canFlush {
				flusher.Flush()
			}
		}
		if n == 0 && complete {
			return
		}
		if n == 0 {
			select {
			case <-fs.ctx.Done():
				panic(http.ErrAbortHandler)
			case <-req.Context().Done():
				panic(http.ErrAbortHandler)
			case <-ticker.C:
			}
		}
	}
}
