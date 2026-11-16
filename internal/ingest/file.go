// Copyright (c) 2026 Costas Kirgoussios
//
// This software is licensed under the PolyForm Noncommercial License 1.0.0.
// You may obtain a copy of the License at:
// https://polyformproject.org/licenses/noncommercial/1.0.0/

package ingest

import (
	"io"
	"io/fs"
	"os"
	"syscall"
)

// OpenFile opens the file at path for reading. It takes the size and the
// modification time of the file at that moment, and the input it returns reads
// no more than that size, so a file that grows during the run is read as it
// was. Anything that is not a regular file (a named pipe, a device) has no
// size and no modification time that mean anything: it is read to its end and
// its ModTime is zero. The io.Closer closes the file; on an error nothing is
// left open.
func OpenFile(path string) (Input, io.Closer, error) {
	f, err := os.Open(path)
	if err != nil {
		return Input{}, nil, err
	}
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return Input{}, nil, err
	}
	switch {
	case fi.IsDir():
		f.Close()
		return Input{}, nil, &fs.PathError{Op: "open", Path: path, Err: syscall.EISDIR}
	case fi.Mode().IsRegular():
		return Input{Name: path, R: io.LimitReader(f, fi.Size()), ModTime: fi.ModTime()}, f, nil
	default:
		return Input{Name: path, R: f}, f, nil
	}
}
