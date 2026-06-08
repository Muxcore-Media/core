package mgr

import (
	"bytes"
	"io"
	"sync"
)

// prefixedWriter wraps an io.Writer and prepends a fixed prefix to every
// line written. This is used to tag sidecar module stdout/stderr output so
// it's distinguishable from core's own log lines, particularly in JSON log
// mode where unprefixed text corrupts the log stream.
type prefixedWriter struct {
	mu     sync.Mutex
	w      io.Writer
	prefix []byte
	buf    []byte // holds incomplete line across Write calls
}

func newPrefixedWriter(w io.Writer, prefix string) *prefixedWriter {
	return &prefixedWriter{w: w, prefix: []byte(prefix)}
}

// Write implements io.Writer. Prefixes each newline-terminated line.
// Partial lines (no trailing newline) are buffered and flushed on the
// next Write that completes them, or on the next call with a newline.
func (p *prefixedWriter) Write(b []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	total := len(b)
	p.buf = append(p.buf, b...)

	for {
		idx := bytes.IndexByte(p.buf, '\n')
		if idx < 0 {
			break
		}
		line := p.buf[:idx+1]
		if _, err := p.w.Write(append(p.prefix, line...)); err != nil {
			return 0, err
		}
		p.buf = p.buf[idx+1:]
	}

	// Flush any remaining partial line without a newline prefix
	// (avoids indefinite buffering of partial lines).
	if len(p.buf) > 4096 {
		if _, err := p.w.Write(append(p.prefix, p.buf...)); err != nil {
			return 0, err
		}
		p.buf = p.buf[:0]
	}

	return total, nil
}
