package storage

import (
	"context"
	"fmt"
	"io"

	"github.com/Muxcore-Media/core/pkg/contracts"
)

// Stream reads a byte range from a storage object.
// If the routed provider implements Streamable, it uses that for efficient
// range requests. Otherwise falls back to Get() with a full read and slice.
// This enables progressive download and in-RAM streaming without requiring
// every provider to implement range requests.
func (o *Orchestrator) Stream(ctx context.Context, key string, offset, length int64) (io.ReadCloser, error) {
	if err := validateKey(key); err != nil {
		return nil, err
	}
	prov, err := o.route(key)
	if err != nil {
		return nil, err
	}

	if streamable, ok := prov.(contracts.Streamable); ok {
		return streamable.Stream(ctx, key, offset, length)
	}

	// Fallback: read the whole object and return a sub-slice.
	rc, err := prov.Get(ctx, key)
	if err != nil {
		return nil, err
	}
	defer rc.Close()

	// Cap fallback read at MaxObjectSize to prevent memory exhaustion (CWE-770).
	data, err := io.ReadAll(io.LimitReader(rc, MaxObjectSize+1))
	if err != nil {
		return nil, fmt.Errorf("stream fallback read: %w", err)
	}
	if int64(len(data)) > MaxObjectSize {
		return nil, fmt.Errorf("object exceeds maximum size %d", MaxObjectSize)
	}

	start := offset
	end := offset + length
	if start < 0 {
		start = 0
	}
	if end > int64(len(data)) || length <= 0 {
		end = int64(len(data))
	}
	if start > int64(len(data)) {
		start = int64(len(data))
	}

	return io.NopCloser(io.NewSectionReader(
		&byteReader{data: data},
		start,
		end-start,
	)), nil
}

// byteReader implements io.ReaderAt for a byte slice.
type byteReader struct {
	data []byte
}

func (r *byteReader) ReadAt(p []byte, off int64) (int, error) {
	if off >= int64(len(r.data)) {
		return 0, io.EOF
	}
	n := copy(p, r.data[off:])
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}
