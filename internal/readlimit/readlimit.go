// SPDX-License-Identifier: MIT

// Package readlimit reads files and streams up to a fixed size: an input that does not
// end (a pipe) or a file that is not what was meant (a log, an image) is refused once it
// passes the limit, before it fills memory. The limits are in SECURITY.md.
package readlimit

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/otfabric/go-ocpp/ocppj"
)

// The limits of ocppctl's inputs, well above any real input of their kind.
const (
	// Payload bounds a payload read from a file or stdin (--payload @file, --payload -,
	// the payload and answer files of a scenario): the largest OCPP-J frame go-ocpp
	// accepts, with room for whitespace around it. A larger payload cannot be sent.
	Payload = ocppj.DefaultMaxFrameSize + 4<<10
	// Secret bounds --password-file and --header-file.
	Secret = 64 << 10
	// Config bounds a station model, a CSMS model (--config) and a scenario.
	Config = 4 << 20
)

// TooLargeError is the error of an input larger than its limit.
type TooLargeError struct {
	Limit int64
}

// Error implements error.
func (e *TooLargeError) Error() string { return "larger than " + Size(e.Limit) }

// Size writes a number of bytes as the documents do: 64 KiB, 4 MiB, or bytes.
func Size(n int64) string {
	switch {
	case n >= 1<<20 && n%(1<<20) == 0:
		return fmt.Sprintf("%d MiB", n>>20)
	case n >= 1<<10 && n%(1<<10) == 0:
		return fmt.Sprintf("%d KiB", n>>10)
	}
	return fmt.Sprintf("%d bytes", n)
}

// Read reads r to its end, at most limit bytes; a longer input is a *TooLargeError, and
// reading stops one byte past the limit.
func Read(r io.Reader, limit int64) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, &TooLargeError{Limit: limit}
	}
	return b, nil
}

// File reads the file at path, at most limit bytes, as Read does.
func File(path string, limit int64) ([]byte, error) {
	f, err := os.Open(filepath.Clean(path))
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return Read(f, limit)
}
