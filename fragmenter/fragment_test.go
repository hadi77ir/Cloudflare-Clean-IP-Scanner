package fragmenter

import (
	"bytes"
	"io"
	"testing"
)

type countingWriter struct {
	writer io.Writer
	count  *int
}

func (w *countingWriter) Write(p []byte) (int, error) {
	if len(p) != 0 {
		(*w.count)++
	}
	return w.writer.Write(p)
}

// newWriter creates a new Writer with the given io.Writer
func newWriter(w io.Writer) (*Writer, *int) {
	count := new(int)
	return &Writer{writer: &countingWriter{writer: w, count: count}, options: Options{ChunkSize: 2, MinimumBytes: 20}}, count
}

func TestWriter_Write(t *testing.T) {
	tests := []struct {
		name     string
		input    []byte
		expected []byte
		count    int
		wantErr  bool
	}{
		{
			name:     "Normal write",
			input:    []byte("Hello, World!"),
			expected: []byte("Hello, World!"),
			count:    7,
			wantErr:  false,
		},
		{
			name:     "Empty write",
			input:    []byte{},
			expected: []byte{},
			count:    0,
			wantErr:  false,
		},
		{
			name:     "Large write",
			input:    bytes.Repeat([]byte("a"), 10000),
			expected: bytes.Repeat([]byte("a"), 10000),
			count:    11,
			wantErr:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			buf := &bytes.Buffer{}
			w, count := newWriter(buf)

			n, err := w.Write(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("Write() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if n != len(tt.input) {
				t.Errorf("Write() n = %v, want %v", n, len(tt.input))
			}
			if !bytes.Equal(buf.Bytes(), tt.expected) {
				t.Errorf("Write() output = %v, want %v", buf.Bytes(), tt.expected)
			}
			if *count != tt.count {
				t.Errorf("Write() counts = %v, want %v", *count, tt.count)
			}
		})
	}
}

func TestWriter_MultipleSequentialWrites(t *testing.T) {
	tests := []struct {
		name       string
		inputs     [][]byte
		expected   []byte
		writeCount int
	}{
		{
			name: "Small writes",
			inputs: [][]byte{
				[]byte("First"),
				[]byte("Second"),
				[]byte("Third"),
			},
			expected: []byte("FirstSecondThird"),
		},
		{
			name: "Mixed size writes",
			inputs: [][]byte{
				[]byte("Short"),
				bytes.Repeat([]byte("a"), 1000),
				[]byte("End"),
			},
			expected: append(append([]byte("Short"), bytes.Repeat([]byte("a"), 1000)...), []byte("End")...),
		},
		{
			name: "Large writes",
			inputs: [][]byte{
				bytes.Repeat([]byte("x"), 5000),
				bytes.Repeat([]byte("y"), 5000),
				bytes.Repeat([]byte("z"), 5000),
			},
			expected: append(append(bytes.Repeat([]byte("x"), 5000), bytes.Repeat([]byte("y"), 5000)...), bytes.Repeat([]byte("z"), 5000)...),
		},
		{
			name: "Empty and non-empty writes",
			inputs: [][]byte{
				[]byte{},
				[]byte("Middle"),
				[]byte{},
			},
			expected: []byte("Middle"),
		},
		{
			name: "Single write",
			inputs: [][]byte{
				[]byte("Single"),
			},
			expected: []byte("Single"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			buf := &bytes.Buffer{}
			w, count := newWriter(buf)

			// Perform sequential writes
			for i, input := range tt.inputs {
				n, err := w.Write(input)
				if err != nil {
					t.Errorf("Sequential write %d error: %v", i, err)
					return
				}
				if n != len(input) {
					t.Errorf("Sequential write %d n = %v, want %v", i, n, len(input))
				}
			}

			// Verify output
			if !bytes.Equal(buf.Bytes(), tt.expected) {
				t.Errorf("Sequential writes output = %v, want %v", buf.Bytes(), tt.expected)
			}
			if *count != tt.writeCount {
				t.Errorf("Sequential writes writeCount = %v, want %v", *count, tt.writeCount)
			}
		})
	}
}
