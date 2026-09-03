package plugin

import (
	"bytes"
	"io"
)

// bytesReader avoids importing bytes at call sites that only need a reader.
func bytesReader(b []byte) *bytes.Reader { return bytes.NewReader(b) }

func bytesClone(b []byte) []byte { return bytes.Clone(b) }

func readAll(r io.Reader) ([]byte, error) { return io.ReadAll(r) }
