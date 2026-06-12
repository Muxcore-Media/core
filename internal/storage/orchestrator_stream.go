package storage

import (
	"bytes"
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
	key = namespaceKey(ctx, key)
	prov, err := o.route(key)
	if err != nil {
		return nil, err
	}

	tctx, cancel := o.withTimeout(ctx, o.readTimeout)
	defer cancel()

	if streamable, ok := prov.(contracts.Streamable); ok {
		return streamable.Stream(tctx, key, offset, length)
	}

	// Fallback: read the whole object and return a sub-slice.
	rc, err := prov.Get(tctx, key)
	if err != nil {
		return nil, err
	}
	if rc == nil {
		return nil, fmt.Errorf("stream fallback: provider returned nil reader without error")
	}
	defer rc.Close()

	// Read the content into memory capped at MaxObjectSize. The context
	// deadline is enforced by tctx which controls the Get call above.
	data, readErr := io.ReadAll(io.LimitReader(rc, MaxObjectSize+1))
	if readErr != nil {
		return nil, fmt.Errorf("stream fallback read: %w", readErr)
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
		bytes.NewReader(data),
		start,
		end-start,
	)), nil
}
