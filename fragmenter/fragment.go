package fragmenter

import (
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultMinimumBytes  = 67
	DefaultFragmentSize  = 47
	DefaultFragmentDelay = time.Duration(100) * time.Millisecond
)

type Options struct {
	// random size for chunks
	RandomChunks bool
	RandomDelays bool
	// if RandomChunks is set to true, this will be count of chunks instead of size
	ChunkSize          int
	DelayBeforeStart   time.Duration
	DelayBetweenChunks time.Duration
	DelayRandomness    time.Duration
	// minimum buffered bytes to be chunked. for TLS hello this should be set to 67
	MinimumBytes int
}

func ParseOptions(opts string) (Options, error) {
	var empty Options
	r := &Options{
		ChunkSize:          DefaultFragmentSize,
		DelayBeforeStart:   DefaultFragmentDelay,
		DelayBetweenChunks: DefaultFragmentDelay,
	}
	// minBytes,chunkSize,beforeStart,betweenChunks,randomChunks,randomDelays,delayRandomness
	parts := strings.Split(opts, ",")
	// first one is always the minBytes
	if len(parts) > 0 {
		if value, err := strconv.Atoi(parts[0]); err == nil {
			r.MinimumBytes = value
		} else {
			return empty, err
		}
	}
	// next is chunk size
	if len(parts) > 1 {
		if value, err := strconv.Atoi(parts[1]); err == nil {
			r.ChunkSize = value
		} else {
			return empty, fmt.Errorf("invalid chunk size: %s", parts[1])
		}
	}
	// next is beforeStart
	if len(parts) > 2 {
		if value, err := time.ParseDuration(parts[2]); err == nil {
			r.DelayBeforeStart = value
		} else {
			return empty, fmt.Errorf("invalid before start delay: %s", parts[2])
		}
	}
	// next is betweenChunks
	if len(parts) > 3 {
		if value, err := time.ParseDuration(parts[3]); err == nil {
			r.DelayBetweenChunks = value
		} else {
			return empty, fmt.Errorf("invalid between chunks delay: %s", parts[3])
		}
	}
	// next is randomChunks
	if len(parts) > 4 {
		if value, err := strconv.ParseBool(parts[4]); err == nil {
			r.RandomChunks = value
		} else {
			return empty, fmt.Errorf("invalid random chunks state: %s", parts[4])
		}
	}
	// next is randomDelays
	if len(parts) > 5 {
		if value, err := strconv.ParseBool(parts[5]); err == nil {
			r.RandomDelays = value
		} else {
			return empty, fmt.Errorf("invalid random delays state: %s", parts[5])
		}
	}
	// next is delayRandomness
	if len(parts) > 6 {
		if value, err := time.ParseDuration(parts[6]); err == nil {
			r.DelayRandomness = value
		} else {
			return empty, fmt.Errorf("invalid delay randomness range: %s", parts[6])
		}
	}

	// validate
	if r.ChunkSize <= 1 {
		return empty, errors.New("chunk size/count should be larger than 1")
	}
	return *r, nil
}

type Writer struct {
	writer       io.Writer
	options      Options
	totalWritten int64
	chunks       []int
	chunkIdx     int
}

// Write implements io.Writer, fragmenting data into chunks with optional delays.
func (w *Writer) Write(p []byte) (int, error) {
	if w.totalWritten == 0 && len(p) > 0 {
		if w.options.DelayBeforeStart > 0 {
			time.Sleep(w.getDelay(w.options.DelayBeforeStart))
		}
	}

	// If we've already written at least MinimumBytes, write directly
	if w.totalWritten >= int64(w.options.MinimumBytes) {
		n, err := w.writer.Write(p)
		w.totalWritten += int64(n)
		return n, err
	}

	// Calculate chunks only for up to MinimumBytes
	if w.chunks == nil {
		totalLength := w.options.MinimumBytes
		if len(p) < totalLength {
			totalLength = len(p)
		}
		w.chunks = w.calculateChunks(totalLength)
	}

	offset := 0
	// Process chunks until MinimumBytes is reached or input is exhausted
	for w.chunkIdx < len(w.chunks) && offset < len(p) && w.totalWritten < int64(w.options.MinimumBytes) {
		remaining := w.chunks[w.chunkIdx]
		if remaining <= 0 {
			w.chunkIdx++
			continue
		}
		toWrite := remaining
		if toWrite > len(p)-offset {
			toWrite = len(p) - offset
		}
		n, err := w.writer.Write(SliceBytes(p, offset, toWrite))
		if err != nil {
			return offset, err
		}
		w.chunks[w.chunkIdx] -= n
		offset += n
		w.totalWritten += int64(n)
		if w.chunks[w.chunkIdx] <= 0 {
			w.chunkIdx++
			if w.chunkIdx < len(w.chunks) && w.options.DelayBetweenChunks > 0 {
				time.Sleep(w.getDelay(w.options.DelayBetweenChunks))
			}
		}
	}

	// If MinimumBytes is reached and there are remaining bytes, write them directly
	if offset < len(p) && w.totalWritten >= int64(w.options.MinimumBytes) {
		n, err := w.writer.Write(p[offset:])
		if err != nil {
			return offset, err
		}
		w.totalWritten += int64(n)
		offset += n
	}

	return offset, nil
}

// SliceBytes safely slices a byte slice from offset with the given length.
func SliceBytes(p []byte, offset int, length int) []byte {
	if offset < 0 || offset >= len(p) || length <= 0 {
		return nil
	}
	if length > len(p)-offset {
		length = len(p) - offset
	}
	return p[offset : offset+length]
}

// calculateChunks computes chunk sizes for the given total length.
func (w *Writer) calculateChunks(totalLength int) []int {
	if !w.options.RandomChunks {
		count := totalLength / w.options.ChunkSize
		if totalLength%w.options.ChunkSize != 0 {
			count++
		}
		chunks := make([]int, count)
		for i := 0; i < count; i++ {
			if i == count-1 && totalLength%w.options.ChunkSize != 0 {
				chunks[i] = totalLength % w.options.ChunkSize
			} else {
				chunks[i] = w.options.ChunkSize
			}
		}
		return chunks
	}

	chunkCount := w.options.ChunkSize
	if chunkCount <= 0 {
		chunkCount = 1
	}
	chunkSizes := make([]int, chunkCount)
	sum := 0
	for i := 0; i < chunkCount; i++ {
		chunkSizes[i] = rand.Intn(1000) + 1 // Avoid zero
		sum += chunkSizes[i]
	}

	remaining := totalLength
	for i := 0; i < chunkCount-1; i++ {
		chunkSizes[i] = (chunkSizes[i] * totalLength) / sum
		remaining -= chunkSizes[i]
	}
	chunkSizes[chunkCount-1] = remaining // Ensure sum equals totalLength
	return chunkSizes
}

// getDelay returns the delay with optional randomness.
func (w *Writer) getDelay(delay time.Duration) time.Duration {
	if w.options.RandomDelays && w.options.DelayRandomness > 0 {
		r := rand.Int63n(int64(w.options.DelayRandomness * 2))
		newDelay := delay + time.Duration(r) - w.options.DelayRandomness
		if newDelay < 0 {
			return 0
		}
		return newDelay
	}
	return delay
}

var _ io.Writer = &Writer{}

type wrappedConn struct {
	net.Conn
	writer io.Writer
}

func (wc *wrappedConn) Write(p []byte) (int, error) {
	return wc.writer.Write(p)
}

func WrapConn(conn net.Conn, options Options) net.Conn {
	return &wrappedConn{
		Conn: conn,
		writer: &Writer{
			writer:  conn,
			options: options,
		},
	}
}
