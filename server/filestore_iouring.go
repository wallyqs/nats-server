// Copyright 2019-2025 The NATS Authors
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

//go:build linux && iouring

package server

import (
	"os"
	iouring "github.com/iceber/iouring-go"
)

// LinuxIOUring wraps the actual io_uring implementation
type LinuxIOUring struct {
	ring *iouring.IOURing
}

func (l *LinuxIOUring) SubmitRequest(request iouring.PrepRequest, ch chan<- iouring.Result) (iouring.Request, error) {
	return l.ring.SubmitRequest(request, ch)
}

func (l *LinuxIOUring) Close() error {
	return l.ring.Close()
}

// initIOUring initializes io_uring for async file operations if enabled and supported
func (fs *fileStore) initIOUring() error {
	if !fs.fcfg.UseIOUring {
		return nil
	}

	// Set default queue depth if not specified
	if fs.fcfg.IOUringQueueDepth == 0 {
		fs.fcfg.IOUringQueueDepth = 64
	}

	// Initialize io_uring ring
	ring, err := iouring.New(uint(fs.fcfg.IOUringQueueDepth))
	if err != nil {
		// Fallback gracefully if io_uring is not available
		fs.fcfg.UseIOUring = false
		return nil
	}

	fs.iour = &LinuxIOUring{ring: ring}
	return nil
}

// closeIOUring closes the io_uring if initialized
func (fs *fileStore) closeIOUring() {
	if fs.iour != nil {
		fs.iour.Close()
		fs.iour = nil
	}
}

// asyncReadAt performs async read using io_uring if available, otherwise falls back to sync read
func (fs *fileStore) asyncReadAt(fd int, buf []byte, offset int64) (int, error) {
	if !fs.fcfg.UseIOUring || fs.iour == nil {
		// Fallback to regular read
		file := os.NewFile(uintptr(fd), "")
		defer file.Close()
		return file.ReadAt(buf, offset)
	}

	fs.iourMu.Lock()
	defer fs.iourMu.Unlock()

	if ring, ok := fs.iour.(*LinuxIOUring); ok {
		ch := make(chan iouring.Result, 1)
		request := iouring.Pread(fd, buf, uint64(offset))
		
		if _, err := ring.SubmitRequest(request, ch); err != nil {
			// Fallback to sync read on error
			file := os.NewFile(uintptr(fd), "")
			defer file.Close()
			return file.ReadAt(buf, offset)
		}

		result := <-ch
		return result.ReturnInt()
	}

	// Fallback to regular read if type assertion fails
	file := os.NewFile(uintptr(fd), "")
	defer file.Close()
	return file.ReadAt(buf, offset)
}

// asyncWriteAt performs async write using io_uring if available, otherwise falls back to sync write
func (fs *fileStore) asyncWriteAt(fd int, buf []byte, offset int64) (int, error) {
	if !fs.fcfg.UseIOUring || fs.iour == nil {
		// Fallback to regular write
		file := os.NewFile(uintptr(fd), "")
		defer file.Close()
		return file.WriteAt(buf, offset)
	}

	fs.iourMu.Lock()
	defer fs.iourMu.Unlock()

	if ring, ok := fs.iour.(*LinuxIOUring); ok {
		ch := make(chan iouring.Result, 1)
		request := iouring.Pwrite(fd, buf, uint64(offset))
		
		if _, err := ring.SubmitRequest(request, ch); err != nil {
			// Fallback to sync write on error
			file := os.NewFile(uintptr(fd), "")
			defer file.Close()
			return file.WriteAt(buf, offset)
		}

		result := <-ch
		return result.ReturnInt()
	}

	// Fallback to regular write if type assertion fails
	file := os.NewFile(uintptr(fd), "")
	defer file.Close()
	return file.WriteAt(buf, offset)
}
