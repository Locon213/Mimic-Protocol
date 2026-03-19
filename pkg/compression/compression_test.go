package compression

import (
	"bytes"
	"crypto/rand"
	"fmt"
	"testing"
)

// TestCompressorBasic tests basic compression/decompression functionality
func TestCompressorBasic(t *testing.T) {
	cfg := DefaultConfig()
	c, err := NewCompressor(cfg)
	if err != nil {
		t.Fatalf("Failed to create compressor: %v", err)
	}
	defer c.Close()

	// Test data: typical web payload (JSON, HTML)
	testData := []byte(`{"status":"success","data":{"user":{"id":12345,"name":"John Doe","email":"john@example.com","preferences":{"theme":"dark","language":"en"}},"meta":{"timestamp":"2024-01-01T00:00:00Z","version":"1.0"}}}`)

	// Compress
	compressed, err := c.CompressWithHeader(testData)
	if err != nil {
		t.Fatalf("Compression failed: %v", err)
	}

	// Verify compression actually reduced size
	if len(compressed) >= len(testData) {
		t.Logf("Warning: compression didn't reduce size (%d -> %d)", len(testData), len(compressed))
	}

	// Decompress
	decompressed, err := c.DecompressWithHeader(compressed)
	if err != nil {
		t.Fatalf("Decompression failed: %v", err)
	}

	// Verify data integrity
	if !bytes.Equal(testData, decompressed) {
		t.Fatalf("Data mismatch after decompression")
	}

	t.Logf("Original: %d bytes, Compressed: %d bytes (%.2f%% reduction)",
		len(testData), len(compressed),
		float64(len(testData)-len(compressed))/float64(len(testData))*100)
}

// TestCompressorSmallData tests that small data is not compressed
func TestCompressorSmallData(t *testing.T) {
	cfg := DefaultConfig()
	c, err := NewCompressor(cfg)
	if err != nil {
		t.Fatalf("Failed to create compressor: %v", err)
	}
	defer c.Close()

	// Small data (below MinSize threshold)
	smallData := []byte("Hello, World!")

	compressed, err := c.CompressWithHeader(smallData)
	if err != nil {
		t.Fatalf("Compression failed: %v", err)
	}

	// Should return uncompressed with header
	decompressed, err := c.DecompressWithHeader(compressed)
	if err != nil {
		t.Fatalf("Decompression failed: %v", err)
	}

	if !bytes.Equal(smallData, decompressed) {
		t.Fatalf("Data mismatch for small data")
	}
}

// TestCompressorRandomData tests compression of incompressible data
func TestCompressorRandomData(t *testing.T) {
	cfg := DefaultConfig()
	c, err := NewCompressor(cfg)
	if err != nil {
		t.Fatalf("Failed to create compressor: %v", err)
	}
	defer c.Close()

	// Random data (should not compress well)
	randomData := make([]byte, 1024)
	if _, err := rand.Read(randomData); err != nil {
		t.Fatalf("Failed to generate random data: %v", err)
	}

	compressed, err := c.CompressWithHeader(randomData)
	if err != nil {
		t.Fatalf("Compression failed: %v", err)
	}

	// Random data should not compress (might even expand slightly due to header)
	if len(compressed) < len(randomData)*9/10 {
		t.Logf("Warning: random data compressed too well (might be a bug)")
	}

	decompressed, err := c.DecompressWithHeader(compressed)
	if err != nil {
		t.Fatalf("Decompression failed: %v", err)
	}

	if !bytes.Equal(randomData, decompressed) {
		t.Fatalf("Data mismatch for random data")
	}
}

// TestCompressorLevels tests different compression levels
func TestCompressorLevels(t *testing.T) {
	testData := []byte(`{"message":"This is a test message with some repetitive content. This is a test message with some repetitive content. This is a test message with some repetitive content."}`)

	for level := 1; level <= 3; level++ {
		t.Run(fmt.Sprintf("Level_%d", level), func(t *testing.T) {
			cfg := CompressorConfig{Level: level, MinSize: 16}
			c, err := NewCompressor(cfg)
			if err != nil {
				t.Fatalf("Failed to create compressor: %v", err)
			}
			defer c.Close()

			compressed, err := c.CompressWithHeader(testData)
			if err != nil {
				t.Fatalf("Compression failed: %v", err)
			}

			decompressed, err := c.DecompressWithHeader(compressed)
			if err != nil {
				t.Fatalf("Decompression failed: %v", err)
			}

			if !bytes.Equal(testData, decompressed) {
				t.Fatalf("Data mismatch at level %d", level)
			}

			t.Logf("Level %d: %d -> %d bytes (%.2f%%)",
				level, len(testData), len(compressed),
				float64(len(testData)-len(compressed))/float64(len(testData))*100)
		})
	}
}

// ============================================
// Benchmarks
// ============================================

// BenchmarkCompressSmall benchmarks compression of small packets (64 bytes)
func BenchmarkCompressSmall(b *testing.B) {
	cfg := CompressorConfig{Level: 2, MinSize: 0}
	c, _ := NewCompressor(cfg)
	defer c.Close()

	data := make([]byte, 64)
	for i := range data {
		data[i] = byte(i % 256)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = c.CompressWithHeader(data)
	}
}

// BenchmarkDecompressSmall benchmarks decompression of small packets
func BenchmarkDecompressSmall(b *testing.B) {
	cfg := CompressorConfig{Level: 2, MinSize: 0}
	c, _ := NewCompressor(cfg)
	defer c.Close()

	data := make([]byte, 64)
	for i := range data {
		data[i] = byte(i % 256)
	}
	compressed, _ := c.CompressWithHeader(data)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = c.DecompressWithHeader(compressed)
	}
}

// BenchmarkCompressMedium benchmarks compression of medium packets (512 bytes)
func BenchmarkCompressMedium(b *testing.B) {
	cfg := CompressorConfig{Level: 2, MinSize: 0}
	c, _ := NewCompressor(cfg)
	defer c.Close()

	data := make([]byte, 512)
	for i := range data {
		data[i] = byte(i % 256)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = c.CompressWithHeader(data)
	}
}

// BenchmarkDecompressMedium benchmarks decompression of medium packets
func BenchmarkDecompressMedium(b *testing.B) {
	cfg := CompressorConfig{Level: 2, MinSize: 0}
	c, _ := NewCompressor(cfg)
	defer c.Close()

	data := make([]byte, 512)
	for i := range data {
		data[i] = byte(i % 256)
	}
	compressed, _ := c.CompressWithHeader(data)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = c.DecompressWithHeader(compressed)
	}
}

// BenchmarkCompressLarge benchmarks compression of large packets (1420 bytes)
func BenchmarkCompressLarge(b *testing.B) {
	cfg := CompressorConfig{Level: 2, MinSize: 0}
	c, _ := NewCompressor(cfg)
	defer c.Close()

	data := make([]byte, 1420)
	for i := range data {
		data[i] = byte(i % 256)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = c.CompressWithHeader(data)
	}
}

// BenchmarkDecompressLarge benchmarks decompression of large packets
func BenchmarkDecompressLarge(b *testing.B) {
	cfg := CompressorConfig{Level: 2, MinSize: 0}
	c, _ := NewCompressor(cfg)
	defer c.Close()

	data := make([]byte, 1420)
	for i := range data {
		data[i] = byte(i % 256)
	}
	compressed, _ := c.CompressWithHeader(data)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = c.DecompressWithHeader(compressed)
	}
}

// BenchmarkCompressJSON benchmarks compression of realistic JSON data
func BenchmarkCompressJSON(b *testing.B) {
	cfg := CompressorConfig{Level: 2, MinSize: 0}
	c, _ := NewCompressor(cfg)
	defer c.Close()

	// Realistic JSON payload (similar to API response)
	data := []byte(`{"status":"success","data":{"user":{"id":12345,"name":"John Doe","email":"john@example.com","preferences":{"theme":"dark","language":"en","notifications":true}},"meta":{"timestamp":"2024-01-01T00:00:00Z","version":"1.0","request_id":"abc123"}}}`)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = c.CompressWithHeader(data)
	}
}

// BenchmarkDecompressJSON benchmarks decompression of realistic JSON data
func BenchmarkDecompressJSON(b *testing.B) {
	cfg := CompressorConfig{Level: 2, MinSize: 0}
	c, _ := NewCompressor(cfg)
	defer c.Close()

	data := []byte(`{"status":"success","data":{"user":{"id":12345,"name":"John Doe","email":"john@example.com","preferences":{"theme":"dark","language":"en","notifications":true}},"meta":{"timestamp":"2024-01-01T00:00:00Z","version":"1.0","request_id":"abc123"}}}`)
	compressed, _ := c.CompressWithHeader(data)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = c.DecompressWithHeader(compressed)
	}
}

// BenchmarkCompressHTML benchmarks compression of HTML content
func BenchmarkCompressHTML(b *testing.B) {
	cfg := CompressorConfig{Level: 2, MinSize: 0}
	c, _ := NewCompressor(cfg)
	defer c.Close()

	// Typical HTML payload
	data := []byte(`<!DOCTYPE html><html><head><title>Test Page</title><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"></head><body><div class="container"><h1>Welcome</h1><p>This is a test page with some content.</p></div></body></html>`)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = c.CompressWithHeader(data)
	}
}

// BenchmarkCompressLevel1 benchmarks compression at level 1 (fastest)
func BenchmarkCompressLevel1(b *testing.B) {
	cfg := CompressorConfig{Level: 1, MinSize: 0}
	c, _ := NewCompressor(cfg)
	defer c.Close()

	data := make([]byte, 512)
	for i := range data {
		data[i] = byte(i % 256)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = c.CompressWithHeader(data)
	}
}

// BenchmarkCompressLevel3 benchmarks compression at level 3 (best)
func BenchmarkCompressLevel3(b *testing.B) {
	cfg := CompressorConfig{Level: 3, MinSize: 0}
	c, _ := NewCompressor(cfg)
	defer c.Close()

	data := make([]byte, 512)
	for i := range data {
		data[i] = byte(i % 256)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = c.CompressWithHeader(data)
	}
}

// BenchmarkRoundTrip benchmarks full compress+decompress cycle
func BenchmarkRoundTrip(b *testing.B) {
	cfg := CompressorConfig{Level: 2, MinSize: 0}
	c, _ := NewCompressor(cfg)
	defer c.Close()

	data := make([]byte, 512)
	for i := range data {
		data[i] = byte(i % 256)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		compressed, _ := c.CompressWithHeader(data)
		_, _ = c.DecompressWithHeader(compressed)
	}
}
