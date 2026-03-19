package mtp

import (
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Locon213/Mimic-Protocol/pkg/compression"
	"golang.org/x/crypto/chacha20poly1305"
)

// Packet types
const (
	PacketDATA   uint8 = 0x01
	PacketACK    uint8 = 0x02
	PacketSYN    uint8 = 0x03
	PacketSYNACK uint8 = 0x04
	PacketFIN    uint8 = 0x05
	PacketFINACK uint8 = 0x06
	PacketPING   uint8 = 0x07
	PacketPONG   uint8 = 0x08
	PacketFEC    uint8 = 0x09
)

// Packet flags
const (
	FlagNone       uint8 = 0x00
	FlagMigrate    uint8 = 0x01 // Session migration
	FlagSACK       uint8 = 0x02 // Selective ACK
	FlagFragment   uint8 = 0x04 // Fragmented payload
	FlagCompressed uint8 = 0x08 // Payload is compressed (applied BEFORE encryption)
)

// MaxPayloadSize is the max payload per MTP packet (safe UDP MTU)
const MaxPayloadSize = 1200

// headerSize is the fixed size of the plaintext header before encryption
const headerSize = 12

// Packet represents a decoded MTP packet
type Packet struct {
	Type       uint8
	SeqNum     uint32
	AckNum     uint32
	Flags      uint8
	Payload    []byte
	SACKBlocks []uint32
}

// Buffer pool for reducing allocations
var (
	encodeBufferPool = sync.Pool{
		New: func() interface{} {
			return make([]byte, 2048)
		},
	}
	decodeBufferPool = sync.Pool{
		New: func() interface{} {
			return make([]byte, 2048)
		},
	}
)

// PacketCodec handles polymorphic encoding and decoding of MTP packets
// with optional compression (compression is applied BEFORE encryption)
type PacketCodec struct {
	sharedKey    []byte // 32-byte key derived from UUID
	dcid         []byte // 8-byte Connection ID for QUIC masking
	dcidRevision atomic.Uint64
	dcidMu       sync.RWMutex

	// Compression (optional, applied before encryption)
	compressor  interface{} // *compression.Compressor or nil
	compressCfg *CompressionConfig
}

// CompressionConfig holds compression configuration
type CompressionConfig struct {
	Enable  bool
	Level   int
	MinSize int
}

// CodecConfig holds configuration for PacketCodec
type CodecConfig struct {
	Secret          string
	EnableDCIDRot   bool               // Enable DCID rotation for security
	DCIDRotInterval int                // DCID rotation interval in seconds
	Compression     *CompressionConfig // Optional compression config
}

// NewPacketCodec creates a new codec with the given shared secret
func NewPacketCodec(secret string) *PacketCodec {
	return NewPacketCodecWithConfig(CodecConfig{
		Secret:          secret,
		EnableDCIDRot:   true,
		DCIDRotInterval: 300, // 5 minutes
		Compression:     nil, // No compression by default
	})
}

// NewPacketCodecWithConfig creates a codec with custom configuration
func NewPacketCodecWithConfig(cfg CodecConfig) *PacketCodec {
	key := deriveKey(cfg.Secret)

	mac := hmac.New(sha256.New, key)
	mac.Write([]byte("QUIC_DCID_DERIVATION"))
	dcid := mac.Sum(nil)[:8]

	codec := &PacketCodec{
		sharedKey:   key,
		dcid:        dcid,
		compressCfg: cfg.Compression,
	}

	// Initialize compressor if compression is enabled
	if cfg.Compression != nil && cfg.Compression.Enable {
		// Lazy initialization will be done in compress/decompress methods
		// to avoid import cycle
	}

	// Start DCID rotation goroutine if enabled
	if cfg.EnableDCIDRot && cfg.DCIDRotInterval > 0 {
		go codec.rotateDCID(cfg.DCIDRotInterval)
	}

	return codec
}

// rotateDCID periodically rotates the DCID for improved security
func (c *PacketCodec) rotateDCID(intervalSeconds int) {
	ticker := time.NewTicker(time.Duration(intervalSeconds) * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		c.dcidMu.Lock()
		revision := c.dcidRevision.Load() + 1
		c.dcidRevision.Store(revision)

		// Derive new DCID with revision
		mac := hmac.New(sha256.New, c.sharedKey)
		mac.Write([]byte("QUIC_DCID_DERIVATION"))
		mac.Write([]byte{byte(revision), byte(revision >> 8), byte(revision >> 16), byte(revision >> 24)})
		newDCID := mac.Sum(nil)[:8]

		c.dcid = newDCID
		c.dcidMu.Unlock()
	}
}

// GetDCID returns current DCID (for debugging)
func (c *PacketCodec) GetDCID() []byte {
	c.dcidMu.RLock()
	defer c.dcidMu.RUnlock()
	return append([]byte(nil), c.dcid...)
}

// deriveKey creates a 32-byte key from a string secret using constant-time operations
func deriveKey(secret string) []byte {
	hash := sha256.Sum256([]byte(secret))
	return hash[:]
}

// constantTimeCompare compares two byte slices in constant time
func constantTimeCompare(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	result := byte(0)
	for i := range a {
		result |= a[i] ^ b[i]
	}
	return result == 0
}

// getAEAD creates or retrieves AEAD cipher
func (c *PacketCodec) getAEAD() (interface{}, error) {
	return chacha20poly1305.NewX(c.sharedKey)
}

// getCompressor lazily initializes the compressor
func (c *PacketCodec) getCompressor() (interface{}, error) {
	if c.compressor != nil {
		return c.compressor, nil
	}

	if c.compressCfg == nil || !c.compressCfg.Enable {
		return nil, nil
	}

	// Import compression package lazily to avoid circular dependency
	// We use reflection-like approach by calling package functions directly
	// This is a workaround - in production you'd import at top level
	level := c.compressCfg.Level
	if level < 1 || level > 3 {
		level = 2
	}

	// Create compressor using the compression package
	// Note: We need to import compression package at the top
	// For now, return nil and handle in Encode/Decode
	return nil, nil
}

// compressPayload compresses payload if compression is enabled
// Returns compressed data or original if compression is disabled/ineffective
func (c *PacketCodec) compressPayload(data []byte) ([]byte, error) {
	if c.compressCfg == nil || !c.compressCfg.Enable {
		return data, nil
	}

	// Don't compress small data
	if len(data) < c.compressCfg.MinSize {
		return data, nil
	}

	// Lazy initialize compressor
	if c.compressor == nil {
		comp, err := c.initCompressor()
		if err != nil {
			return data, err
		}
		if comp == nil {
			return data, nil
		}
		c.compressor = comp
	}

	// Type assert to compression.Compressor
	comp, ok := c.compressor.(*compression.Compressor)
	if !ok {
		return data, nil
	}

	compressed, err := comp.CompressWithHeader(data)
	if err != nil {
		return data, err
	}

	// Only return compressed if it's actually smaller
	// (CompressWithHeader adds 5-byte header, so might not be worth it for small data)
	if len(compressed) >= len(data) {
		return data, nil
	}

	return compressed, nil
}

// decompressPayload decompresses payload if it was compressed
func (c *PacketCodec) decompressPayload(data []byte) ([]byte, error) {
	if c.compressCfg == nil || !c.compressCfg.Enable {
		return data, nil
	}

	// Lazy initialize compressor
	if c.compressor == nil {
		comp, err := c.initCompressor()
		if err != nil {
			return data, err
		}
		if comp == nil {
			return data, nil
		}
		c.compressor = comp
	}

	// Type assert to compression.Compressor
	comp, ok := c.compressor.(*compression.Compressor)
	if !ok {
		return data, nil
	}

	return comp.DecompressWithHeader(data)
}

// initCompressor initializes the zstd compressor
func (c *PacketCodec) initCompressor() (*compression.Compressor, error) {
	if c.compressCfg == nil {
		return nil, nil
	}

	level := c.compressCfg.Level
	if level < 1 || level > 3 {
		level = 2
	}

	return compression.NewCompressor(compression.CompressorConfig{
		Level:   level,
		MinSize: c.compressCfg.MinSize,
	})
}

// Encode serializes and encrypts a Packet into wire format:
// Pipeline: Payload -> Compress (optional) -> Encrypt -> Add QUIC header
// [ QUIC Short Header: 9 bytes ] [ padLen: 2 ] [ padding: N ] [ nonce: 24 ] [ ciphertext: M ]
func (c *PacketCodec) Encode(pkt *Packet) ([]byte, error) {
	// 1. Compress payload FIRST (before encryption) - compression is transparent
	var payloadBytes []byte
	var err error
	wasCompressed := false

	if c.compressCfg != nil && c.compressCfg.Enable && len(pkt.Payload) > 0 {
		payloadBytes, err = c.compressPayload(pkt.Payload)
		if err != nil {
			return nil, fmt.Errorf("mtp: compression failed: %w", err)
		}
		// Only set FlagCompressed if data was actually compressed (size reduced)
		if len(payloadBytes) < len(pkt.Payload) {
			pkt.Flags |= FlagCompressed
			wasCompressed = true
		}
	} else {
		payloadBytes = pkt.Payload
	}

	_ = wasCompressed // for debugging

	// Get buffer from pool for plaintext (before encryption)
	plaintextBuf := encodeBufferPool.Get().([]byte)
	defer encodeBufferPool.Put(plaintextBuf)

	// 2. Build plaintext header at the START of buffer
	plaintextBuf[0] = pkt.Type
	binary.BigEndian.PutUint32(plaintextBuf[1:5], pkt.SeqNum)
	binary.BigEndian.PutUint32(plaintextBuf[5:9], pkt.AckNum)
	binary.BigEndian.PutUint16(plaintextBuf[9:11], uint16(len(payloadBytes)))
	plaintextBuf[11] = pkt.Flags

	plaintextLen := headerSize + len(payloadBytes)
	copy(plaintextBuf[headerSize:headerSize+len(payloadBytes)], payloadBytes)

	// Add SACK blocks if present
	if pkt.Flags&FlagSACK != 0 && len(pkt.SACKBlocks) > 0 {
		sackStart := plaintextLen
		binary.BigEndian.PutUint16(plaintextBuf[sackStart:sackStart+2], uint16(len(pkt.SACKBlocks)))
		for i, seq := range pkt.SACKBlocks {
			binary.BigEndian.PutUint32(plaintextBuf[sackStart+2+i*4:sackStart+6+i*4], seq)
		}
		plaintextLen += 2 + len(pkt.SACKBlocks)*4
	}

	// 3. Encrypt with ChaCha20-Poly1305
	aeadIface, err := c.getAEAD()
	if err != nil {
		return nil, fmt.Errorf("mtp: failed to create cipher: %w", err)
	}
	aead := aeadIface.(cipher.AEAD)

	nonceSize := aead.NonceSize()
	nonce := make([]byte, nonceSize)
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("mtp: failed to generate nonce: %w", err)
	}

	// Encrypt plaintext (separate buffer to avoid overlap)
	ciphertext := aead.Seal(nil, nonce, plaintextBuf[:plaintextLen], nil)
	ciphertextLen := len(ciphertext)

	// 3. Generate smart junk padding
	baseSize := 2 + nonceSize + ciphertextLen
	maxPad := 1350 - baseSize
	if maxPad < 10 {
		maxPad = 10
	}

	randBytes := make([]byte, 2)
	if _, err := rand.Read(randBytes); err != nil {
		return nil, err
	}
	rng := binary.BigEndian.Uint16(randBytes)

	// 50% chance to pad up to MTU, 50% chance for small padding
	var padLen int
	if rng%2 == 0 {
		padLen = int(rng%uint16(maxPad)) + 1
	} else {
		padLen = int(rng%32) + 1
	}

	padding := make([]byte, padLen)
	if _, err := rand.Read(padding); err != nil {
		return nil, err
	}

	// 4. Get DCID for QUIC header
	c.dcidMu.RLock()
	dcid := append([]byte(nil), c.dcid...)
	c.dcidMu.RUnlock()

	// 5. Build final wire packet
	wireLen := 9 + 2 + padLen + nonceSize + ciphertextLen
	wire := make([]byte, wireLen)

	offset := 0
	// QUIC Short Header (9 bytes)
	wire[offset] = 0x40
	copy(wire[offset+1:offset+9], dcid)
	offset += 9

	// PadLen (2 bytes)
	binary.BigEndian.PutUint16(wire[offset:offset+2], uint16(padLen))
	offset += 2

	// Padding (padLen bytes)
	copy(wire[offset:offset+padLen], padding)
	offset += padLen

	// Nonce (nonceSize bytes)
	copy(wire[offset:offset+nonceSize], nonce)
	offset += nonceSize

	// Ciphertext
	copy(wire[offset:], ciphertext)

	return wire, nil
}

// Decode decrypts and deserializes wire bytes into a Packet
func (c *PacketCodec) Decode(wire []byte) (*Packet, error) {
	if len(wire) < 9+3 {
		return nil, fmt.Errorf("mtp: packet too short")
	}

	// 1. Verify QUIC Short Header
	if wire[0] != 0x40 {
		return nil, fmt.Errorf("mtp: invalid QUIC short header flag")
	}

	// 2. Constant-time DCID comparison
	c.dcidMu.RLock()
	dcid := append([]byte(nil), c.dcid...)
	c.dcidMu.RUnlock()

	// Use constant-time comparison to prevent timing attacks
	if !constantTimeCompare(wire[1:9], dcid) {
		return nil, fmt.Errorf("mtp: DCID mismatch (active probe?)")
	}

	offset := 9

	// 3. Read padding length
	padLen := int(binary.BigEndian.Uint16(wire[offset : offset+2]))
	if padLen < 1 || padLen > 1350 {
		return nil, fmt.Errorf("mtp: invalid padding length %d", padLen)
	}

	aeadIface, err := c.getAEAD()
	if err != nil {
		return nil, fmt.Errorf("mtp: failed to create cipher: %w", err)
	}
	aead := aeadIface.(cipher.AEAD)

	nonceSize := aead.NonceSize()
	minLen := offset + 2 + padLen + nonceSize + aead.Overhead() + headerSize
	if len(wire) < minLen {
		return nil, fmt.Errorf("mtp: packet too short for declared padding")
	}

	// 4. Extract nonce and ciphertext
	dataOffset := offset + 2 + padLen
	nonce := wire[dataOffset : dataOffset+nonceSize]
	ciphertext := wire[dataOffset+nonceSize:]

	// Get buffer from pool for plaintext
	plaintextBuf := decodeBufferPool.Get().([]byte)
	defer decodeBufferPool.Put(plaintextBuf)

	// Decrypt
	plaintext, err := aead.Open(plaintextBuf[:0], nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("mtp: decryption failed: %w", err)
	}

	if len(plaintext) < headerSize {
		return nil, fmt.Errorf("mtp: plaintext too short for header")
	}

	// 5. Parse header
	pkt := &Packet{
		Type:   plaintext[0],
		SeqNum: binary.BigEndian.Uint32(plaintext[1:5]),
		AckNum: binary.BigEndian.Uint32(plaintext[5:9]),
		Flags:  plaintext[11],
	}

	payloadLen := int(binary.BigEndian.Uint16(plaintext[9:11]))
	remaining := plaintext[headerSize:]

	if len(remaining) < payloadLen {
		return nil, fmt.Errorf("mtp: payload length mismatch")
	}

	// Copy payload to avoid retaining reference to pool buffer
	rawPayload := make([]byte, payloadLen)
	copy(rawPayload, remaining[:payloadLen])

	// 6. Decompress if FlagCompressed is set (after decryption)
	if pkt.Flags&FlagCompressed != 0 {
		decompressed, err := c.decompressPayload(rawPayload)
		if err != nil {
			return nil, fmt.Errorf("mtp: decompression failed: %w", err)
		}
		pkt.Payload = decompressed
		// Clear the compressed flag after decompression
		pkt.Flags &^= FlagCompressed
	} else {
		pkt.Payload = rawPayload
	}

	// 7. Parse SACK blocks if present
	if pkt.Flags&FlagSACK != 0 {
		sackData := remaining[payloadLen:]
		if len(sackData) >= 2 {
			sackCount := int(binary.BigEndian.Uint16(sackData[0:2]))
			if len(sackData) >= 2+sackCount*4 {
				pkt.SACKBlocks = make([]uint32, sackCount)
				for i := 0; i < sackCount; i++ {
					pkt.SACKBlocks[i] = binary.BigEndian.Uint32(sackData[2+i*4:])
				}
			}
		}
	}

	return pkt, nil
}

// packetTypeName returns a human-readable name for a packet type
func packetTypeName(t uint8) string {
	switch t {
	case PacketDATA:
		return "DATA"
	case PacketACK:
		return "ACK"
	case PacketSYN:
		return "SYN"
	case PacketSYNACK:
		return "SYN-ACK"
	case PacketFIN:
		return "FIN"
	case PacketFINACK:
		return "FIN-ACK"
	case PacketPING:
		return "PING"
	case PacketPONG:
		return "PONG"
	default:
		return fmt.Sprintf("UNKNOWN(%02x)", t)
	}
}
