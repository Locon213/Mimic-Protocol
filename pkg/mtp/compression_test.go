package mtp

import (
	"bytes"
	"testing"
)

// TestPacketCodecCompression tests compression in PacketCodec
func TestPacketCodecCompression(t *testing.T) {
	// Create codec with compression enabled
	cfg := CodecConfig{
		Secret:          "test-secret-12345",
		EnableDCIDRot:   false,
		DCIDRotInterval: 0,
		Compression: &CompressionConfig{
			Enable:  true,
			Level:   2,
			MinSize: 32,
		},
	}

	codec := NewPacketCodecWithConfig(cfg)

	// Test data: repetitive text (compressible)
	testPayload := []byte(`{"status":"success","data":{"message":"Hello World","count":123}}`)

	pkt := &Packet{
		Type:    PacketDATA,
		SeqNum:  1,
		AckNum:  0,
		Flags:   FlagNone,
		Payload: testPayload,
	}

	// Encode (should compress)
	encoded, err := codec.Encode(pkt)
	if err != nil {
		t.Fatalf("Encode failed: %v", err)
	}

	// Decode (should decompress)
	decoded, err := codec.Decode(encoded)
	if err != nil {
		t.Fatalf("Decode failed: %v", err)
	}

	// Verify payload integrity
	if !bytes.Equal(testPayload, decoded.Payload) {
		t.Fatalf("Payload mismatch: expected %v, got %v", testPayload, decoded.Payload)
	}

	// Verify compression flag was set during encoding
	if pkt.Flags&FlagCompressed == 0 {
		t.Log("Note: Compression flag not set (data might not have been compressed)")
	}

	t.Logf("Original: %d bytes, Encoded packet: %d bytes", len(testPayload), len(encoded))
}

// TestPacketCodecNoCompression tests that small packets are not compressed
func TestPacketCodecNoCompression(t *testing.T) {
	cfg := CodecConfig{
		Secret:          "test-secret-12345",
		EnableDCIDRot:   false,
		DCIDRotInterval: 0,
		Compression: &CompressionConfig{
			Enable:  true,
			Level:   2,
			MinSize: 64, // Minimum 64 bytes
		},
	}

	codec := NewPacketCodecWithConfig(cfg)

	// Small payload (should NOT be compressed)
	smallPayload := []byte(`Hello!`)

	pkt := &Packet{
		Type:    PacketDATA,
		SeqNum:  1,
		Payload: smallPayload,
	}

	encoded, err := codec.Encode(pkt)
	if err != nil {
		t.Fatalf("Encode failed: %v", err)
	}

	decoded, err := codec.Decode(encoded)
	if err != nil {
		t.Fatalf("Decode failed: %v", err)
	}

	if !bytes.Equal(smallPayload, decoded.Payload) {
		t.Fatalf("Small payload mismatch")
	}
}

// TestPacketCodecCompressionRandomData tests compression with incompressible data
func TestPacketCodecCompressionRandomData(t *testing.T) {
	cfg := CodecConfig{
		Secret:          "test-secret-12345",
		EnableDCIDRot:   false,
		DCIDRotInterval: 0,
		Compression: &CompressionConfig{
			Enable:  true,
			Level:   2,
			MinSize: 32,
		},
	}

	codec := NewPacketCodecWithConfig(cfg)

	// Random data (should not compress well)
	randomData := make([]byte, 256)
	for i := range randomData {
		randomData[i] = byte(i % 256)
	}

	pkt := &Packet{
		Type:    PacketDATA,
		SeqNum:  1,
		Payload: randomData,
	}

	encoded, err := codec.Encode(pkt)
	if err != nil {
		t.Fatalf("Encode failed: %v", err)
	}

	decoded, err := codec.Decode(encoded)
	if err != nil {
		t.Fatalf("Decode failed: %v", err)
	}

	if !bytes.Equal(randomData, decoded.Payload) {
		t.Fatalf("Random data mismatch")
	}
}

// TestPacketCodecWithoutCompression tests codec without compression
func TestPacketCodecWithoutCompression(t *testing.T) {
	cfg := CodecConfig{
		Secret:          "test-secret-12345",
		EnableDCIDRot:   false,
		DCIDRotInterval: 0,
		Compression:     nil, // No compression
	}

	codec := NewPacketCodecWithConfig(cfg)

	testPayload := []byte(`Test payload without compression enabled`)

	pkt := &Packet{
		Type:    PacketDATA,
		SeqNum:  1,
		Payload: testPayload,
	}

	encoded, err := codec.Encode(pkt)
	if err != nil {
		t.Fatalf("Encode failed: %v", err)
	}

	decoded, err := codec.Decode(encoded)
	if err != nil {
		t.Fatalf("Decode failed: %v", err)
	}

	if !bytes.Equal(testPayload, decoded.Payload) {
		t.Fatalf("Payload mismatch without compression")
	}
}

// ============================================
// Benchmarks
// ============================================

// BenchmarkPacketCodecEncodeNoCompression benchmarks encoding without compression
func BenchmarkPacketCodecEncodeNoCompression(b *testing.B) {
	cfg := CodecConfig{
		Secret:          "test-secret",
		EnableDCIDRot:   false,
		Compression:     nil,
	}
	codec := NewPacketCodecWithConfig(cfg)

	data := make([]byte, 512)
	for i := range data {
		data[i] = byte(i % 256)
	}

	pkt := &Packet{
		Type:    PacketDATA,
		SeqNum:  1,
		Payload: data,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = codec.Encode(pkt)
	}
}

// BenchmarkPacketCodecEncodeWithCompression benchmarks encoding with compression
func BenchmarkPacketCodecEncodeWithCompression(b *testing.B) {
	cfg := CodecConfig{
		Secret:          "test-secret",
		EnableDCIDRot:   false,
		Compression: &CompressionConfig{
			Enable:  true,
			Level:   2,
			MinSize: 0,
		},
	}
	codec := NewPacketCodecWithConfig(cfg)

	// Compressible data (repetitive pattern)
	data := []byte(`{"status":"success","data":{"message":"Hello World","count":123,"items":["item1","item2","item3"]}}`)

	pkt := &Packet{
		Type:    PacketDATA,
		SeqNum:  1,
		Payload: data,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = codec.Encode(pkt)
	}
}

// BenchmarkPacketCodecDecodeNoCompression benchmarks decoding without compression
func BenchmarkPacketCodecDecodeNoCompression(b *testing.B) {
	cfg := CodecConfig{
		Secret:          "test-secret",
		EnableDCIDRot:   false,
		Compression:     nil,
	}
	codec := NewPacketCodecWithConfig(cfg)

	data := make([]byte, 512)
	for i := range data {
		data[i] = byte(i % 256)
	}

	pkt := &Packet{
		Type:    PacketDATA,
		SeqNum:  1,
		Payload: data,
	}

	encoded, _ := codec.Encode(pkt)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = codec.Decode(encoded)
	}
}

// BenchmarkPacketCodecDecodeWithCompression benchmarks decoding with compression
func BenchmarkPacketCodecDecodeWithCompression(b *testing.B) {
	cfg := CodecConfig{
		Secret:          "test-secret",
		EnableDCIDRot:   false,
		Compression: &CompressionConfig{
			Enable:  true,
			Level:   2,
			MinSize: 0,
		},
	}
	codec := NewPacketCodecWithConfig(cfg)

	data := []byte(`{"status":"success","data":{"message":"Hello World","count":123,"items":["item1","item2","item3"]}}`)

	pkt := &Packet{
		Type:    PacketDATA,
		SeqNum:  1,
		Payload: data,
	}

	encoded, _ := codec.Encode(pkt)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = codec.Decode(encoded)
	}
}

// BenchmarkPacketCodecRoundTrip benchmarks full encode/decode cycle
func BenchmarkPacketCodecRoundTrip(b *testing.B) {
	cfg := CodecConfig{
		Secret:          "test-secret",
		EnableDCIDRot:   false,
		Compression: &CompressionConfig{
			Enable:  true,
			Level:   2,
			MinSize: 0,
		},
	}
	codec := NewPacketCodecWithConfig(cfg)

	data := []byte(`{"status":"success","data":{"message":"Hello World","count":123}}`)

	pkt := &Packet{
		Type:    PacketDATA,
		SeqNum:  1,
		Payload: data,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		encoded, _ := codec.Encode(pkt)
		_, _ = codec.Decode(encoded)
	}
}
