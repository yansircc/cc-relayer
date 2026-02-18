package relay

import (
	"bufio"
	"io"
)

// SSEScanner reads Server-Sent Events line by line.
type SSEScanner struct {
	scanner *bufio.Scanner
}

func NewSSEScanner(r io.Reader) *SSEScanner {
	s := bufio.NewScanner(r)
	s.Buffer(make([]byte, 0, 256*1024), 1024*1024) // 1MB max line
	return &SSEScanner{scanner: s}
}

func (s *SSEScanner) Scan() bool {
	return s.scanner.Scan()
}

func (s *SSEScanner) Text() string {
	return s.scanner.Text()
}

func (s *SSEScanner) Err() error {
	return s.scanner.Err()
}
