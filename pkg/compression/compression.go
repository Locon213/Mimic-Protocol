package compression

import (
	"sync"

	"github.com/klauspost/compress/zstd"
)

// Compressor provides fast compression/decompression using zstd
type Compressor struct {
	encoder *zstd.Encoder
	decoder *zstd.Decoder
	pool    sync.Pool
}

// CompressorConfig holds configuration for the compressor
type CompressorConfig struct {
	// Compression level (1-3, higher = better compression but slower)
	// 1 = Fastest, 2 = Default, 3 = Better compression
	Level int
	// MinSize is the minimum size to attempt compression
	// Data smaller than this won't be compressed
	MinSize int
}

// DefaultConfig returns default compressor configuration
func DefaultConfig() CompressorConfig {
	return CompressorConfig{
		Level:   2,
		MinSize: 64, // Don't compress small packets
	}
}

// NewCompressor creates a new compressor instance
func NewCompressor(cfg CompressorConfig) (*Compressor, error) {
	if cfg.Level < 1 || cfg.Level > 3 {
		cfg.Level = 2
	}

	encoder, err := zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.EncoderLevel(cfg.Level)))
	if err != nil {
		return nil, err
	}

	decoder, err := zstd.NewReader(nil)
	if err != nil {
		encoder.Close()
		return nil, err
	}

	c := &Compressor{
		encoder: encoder,
		decoder: decoder,
		pool: sync.Pool{
			New: func() interface{} {
				return make([]byte, 0, 4096)
			},
		},
	}

	return c, nil
}

// Compress compresses data and returns the compressed bytes
// Returns original data if compression doesn't reduce size
func (c *Compressor) Compress(data []byte) ([]byte, error) {
	// Don't compress small data
	if len(data) < c.MinSize() {
		result := make([]byte, len(data))
		copy(result, data)
		return result, nil
	}

	// Get buffer from pool
	buf := c.pool.Get().([]byte)
	defer func() {
		buf = buf[:0]
		c.pool.Put(buf)
	}()

	// Compress
	compressed := c.encoder.EncodeAll(data, buf)

	// Only return compressed if smaller
	if len(compressed) >= len(data) {
		result := make([]byte, len(data))
		copy(result, data)
		return result, nil
	}

	// Add header to indicate compressed
	result := make([]byte, 1+len(compressed))
	result[0] = 1 // compressed flag
	copy(result[1:], compressed)
	return result, nil
}

// Decompress decompresses data
func (c *Compressor) Decompress(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return data, nil
	}

	// Check if compressed
	if data[0] == 1 {
		decompressed, err := c.decoder.DecodeAll(data[1:], nil)
		if err != nil {
			return nil, err
		}
		return decompressed, nil
	}

	// Not compressed
	result := make([]byte, len(data))
	copy(result, data)
	return result, nil
}

// MinSize returns the minimum size for compression
func (c *Compressor) MinSize() int {
	return DefaultConfig().MinSize
}

// Close releases resources
func (c *Compressor) Close() {
	if c.encoder != nil {
		c.encoder.Close()
	}
	if c.decoder != nil {
		c.decoder.Close()
	}
}

// CompressWithHeader compresses data and adds a header with original size
// Format: [compressed: 1 byte][originalSize: 4 bytes][compressedData: N bytes]
func (c *Compressor) CompressWithHeader(data []byte) ([]byte, error) {
	if len(data) < c.MinSize() {
		// Return uncompressed with flag
		result := make([]byte, 5+len(data))
		result[0] = 0 // uncompressed flag
		// original size in bytes 1-4
		result[1] = byte(len(data) >> 24)
		result[2] = byte(len(data) >> 16)
		result[3] = byte(len(data) >> 8)
		result[4] = byte(len(data))
		copy(result[5:], data)
		return result, nil
	}

	buf := c.pool.Get().([]byte)
	defer func() {
		buf = buf[:0]
		c.pool.Put(buf)
	}()

	compressed := c.encoder.EncodeAll(data, buf)

	// If compression ratio is poor, return uncompressed
	if len(compressed) >= len(data)*9/10 {
		result := make([]byte, 5+len(data))
		result[0] = 0 // uncompressed
		result[1] = byte(len(data) >> 24)
		result[2] = byte(len(data) >> 16)
		result[3] = byte(len(data) >> 8)
		result[4] = byte(len(data))
		copy(result[5:], data)
		return result, nil
	}

	// Return compressed with header
	result := make([]byte, 5+len(compressed))
	result[0] = 1 // compressed flag
	result[1] = byte(len(data) >> 24)
	result[2] = byte(len(data) >> 16)
	result[3] = byte(len(data) >> 8)
	result[4] = byte(len(data))
	copy(result[5:], compressed)
	return result, nil
}

// DecompressWithHeader decompresses data with header
func (c *Compressor) DecompressWithHeader(data []byte) ([]byte, error) {
	if len(data) < 5 {
		return data, nil
	}

	// Read original size
	originalSize := int(data[1])<<24 | int(data[2])<<16 | int(data[3])<<8 | int(data[4])

	if data[0] == 0 {
		// Uncompressed
		result := make([]byte, originalSize)
		copy(result, data[5:5+originalSize])
		return result, nil
	}

	// Compressed
	decompressed, err := c.decoder.DecodeAll(data[5:], nil)
	if err != nil {
		return nil, err
	}
	return decompressed, nil
}

// CompressionRatio returns compression ratio for given data
func (c *Compressor) CompressionRatio(data []byte) (float64, error) {
	compressed, err := c.Compress(data)
	if err != nil {
		return 0, err
	}
	return float64(len(compressed)) / float64(len(data)), nil
}
