//go:build !linux || !iouring

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

package server

import "os"

// initIOUring is a no-op on non-Linux platforms
func (fs *fileStore) initIOUring() error {
	// io_uring is Linux-only, disable it on other platforms
	fs.fcfg.UseIOUring = false
	return nil
}

// closeIOUring is a no-op on non-Linux platforms
func (fs *fileStore) closeIOUring() {
	// No-op on non-Linux platforms
}

// asyncReadAt falls back to synchronous read on non-Linux platforms
func (fs *fileStore) asyncReadAt(fd int, buf []byte, offset int64) (int, error) {
	file := os.NewFile(uintptr(fd), "")
	defer file.Close()
	return file.ReadAt(buf, offset)
}

// asyncWriteAt falls back to synchronous write on non-Linux platforms
func (fs *fileStore) asyncWriteAt(fd int, buf []byte, offset int64) (int, error) {
	file := os.NewFile(uintptr(fd), "")
	defer file.Close()
	return file.WriteAt(buf, offset)
}