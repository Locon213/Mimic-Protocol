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
// with QUIC-like wire format for anti-DPI mimickry.
// Wire format mimics real QUIC (RFC 9000):
//
//	Initial (SYN):  Long header  → [0xC0|pnLen] [Version] [DCID] [SCID] [TokenLen] [Token] [Len] [PN] [encrypted]
//	1-RTT (other):  Short header → [0x40|pnLen] [DCID] [PN] [encrypted]
type PacketCodec struct {
	sharedKey    []byte // 32-byte key derived from UUID
	dcid         []byte // 8-byte Destination Connection ID (QUIC masking)
	scid         []byte // 8-byte Source Connection ID (QUIC masking)
	dcidRevision atomic.Uint64
	dcidMu       sync.RWMutex
	stopCh       chan struct{} // Closed to stop background goroutines

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

	// Generate SCID from a different derivation for client/server differentiation
	mac2 := hmac.New(sha256.New, key)
	mac2.Write([]byte("QUIC_SCID_DERIVATION"))
	scid := mac2.Sum(nil)[:8]

	codec := &PacketCodec{
		sharedKey:   key,
		dcid:        dcid,
		scid:        scid,
		compressCfg: cfg.Compression,
		stopCh:      make(chan struct{}),
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

// Close stops background goroutines
func (c *PacketCodec) Close() {
	select {
	case <-c.stopCh:
		// Already closed
	default:
		close(c.stopCh)
	}
}

// rotateDCID periodically rotates the DCID for improved security
func (c *PacketCodec) rotateDCID(intervalSeconds int) {
	ticker := time.NewTicker(time.Duration(intervalSeconds) * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-c.stopCh:
			return
		case <-ticker.C:
			c.dcidMu.Lock()
			revision := c.dcidRevision.Load() + 1
			c.dcidRevision.Store(revision)

			revBytes := []byte{byte(revision), byte(revision >> 8), byte(revision >> 16), byte(revision >> 24)}

			mac := hmac.New(sha256.New, c.sharedKey)
			mac.Write([]byte("QUIC_DCID_DERIVATION"))
			mac.Write(revBytes)
			c.dcid = mac.Sum(nil)[:8]

			mac2 := hmac.New(sha256.New, c.sharedKey)
			mac2.Write([]byte("QUIC_SCID_DERIVATION"))
			mac2.Write(revBytes)
			c.scid = mac2.Sum(nil)[:8]

			c.dcidMu.Unlock()
		}
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

// Encode serializes and encrypts a Packet into QUIC-like wire format.
//
// SYN/SYNACK → QUIC Initial (Long Header):
//
//	[0xC0|pnLen:1][Version:4][DCIDLen:1][DCID:8][SCIDLen:1][SCID:8]
//	[TokenLen:1][Token:0][PayloadLen:varint][PN:1..4][encrypted_payload]
//
// DATA/ACK/FIN/PING/etc → QUIC 1-RTT (Short Header):
//
//	[0x40|pnLen:1][DCID:8][PN:1..4][encrypted_payload]
func (c *PacketCodec) Encode(pkt *Packet) ([]byte, error) {
	// 1. Compress payload FIRST (before encryption)
	var payloadBytes []byte
	var err error
	wasCompressed := false

	if c.compressCfg != nil && c.compressCfg.Enable && len(pkt.Payload) > 0 {
		payloadBytes, err = c.compressPayload(pkt.Payload)
		if err != nil {
			return nil, fmt.Errorf("mtp: compression failed: %w", err)
		}
		if len(payloadBytes) < len(pkt.Payload) {
			pkt.Flags |= FlagCompressed
			wasCompressed = true
		}
	} else {
		payloadBytes = pkt.Payload
	}
	_ = wasCompressed

	// 2. Build MTP plaintext header + payload
	plaintextBuf := encodeBufferPool.Get().([]byte)
	defer encodeBufferPool.Put(plaintextBuf)

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

	ciphertext := aead.Seal(nil, nonce, plaintextBuf[:plaintextLen], nil)
	// Encrypted format inside wire: [nonce:24][ciphertext+tag]
	encPayload := make([]byte, nonceSize+len(ciphertext))
	copy(encPayload, nonce)
	copy(encPayload[nonceSize:], ciphertext)
	encLen := len(encPayload)

	// 4. Get DCID and SCID
	c.dcidMu.RLock()
	dcid := append([]byte(nil), c.dcid...)
	scid := append([]byte(nil), c.scid...)
	c.dcidMu.RUnlock()

	// 5. Build QUIC-like wire packet
	isInitial := pkt.Type == PacketSYN || pkt.Type == PacketSYNACK
	pnLen := 1 // Packet number length (1 byte is enough for MTP)

	var wire []byte
	if isInitial {
		// QUIC Initial Long Header (RFC 9000 Section 17.2.2)
		// Fixed header: 0xC0 = 1100_0000 (HeaderForm=1, FixedBit=1, LongPacketType=0=Initial, PN length=00 → will set below)
		// Header byte: 0xC0 | (pnLen - 1)  — bottom 2 bits = packet number length
		headerByte := byte(0xC0) | byte(pnLen-1)

		// QUIC Version (4 bytes) — use QUIC v1 (RFC 9000)
		quicVersion := uint32(0x00000001)

		// Token is empty for our use case
		tokenLen := 0

		// Calculate header size (up to and including varint, excluding PN — PN is part of varint length)
		// [header:1][version:4][dcidLen:1][dcid:8][scidLen:1][scid:8][tokenLen:1][token:0][length:varint(2)]
		headerSize := 1 + 4 + 1 + 8 + 1 + 8 + 1 + tokenLen + 2 // = 26 bytes (without PN)

		// Padding: QUIC Initial packets should be padded to look realistic
		// But without a separate payload length field, padding can't be separated from ciphertext.
		// The QUIC header structure (long header, version, DCID/SCID) provides the mimickry.
		padNeeded := 0

		wire = make([]byte, headerSize+pnLen+encLen+padNeeded)
		off := 0

		wire[off] = headerByte
		off++

		binary.BigEndian.PutUint32(wire[off:off+4], quicVersion)
		off += 4

		wire[off] = byte(len(dcid))
		off++
		copy(wire[off:off+8], dcid)
		off += 8

		wire[off] = byte(len(scid))
		off++
		copy(wire[off:off+8], scid)
		off += 8

		wire[off] = byte(tokenLen) // 0
		off++

		// Length: varint encoded (PN + encrypted payload + padding)
		remaining := pnLen + encLen + padNeeded
		if remaining > 16383 {
			return nil, fmt.Errorf("mtp: initial packet payload too large for varint")
		}
		encodeVarint(wire[off:off+2], uint64(remaining))
		off += 2

		// Packet number (1 byte) — use SeqNum mod 256
		wire[off] = byte(pkt.SeqNum)
		off++

		// Encrypted payload
		copy(wire[off:off+encLen], encPayload)
		off += encLen

		// Padding (random bytes, looks like encrypted data)
		if padNeeded > 0 {
			if _, err := rand.Read(wire[off : off+padNeeded]); err != nil {
				return nil, err
			}
		}
	} else {
		// QUIC 1-RTT Short Header (RFC 9000 Section 17.3)
		// Fixed header: 0x40 = 0100_0000 (HeaderForm=0, FixedBit=1, Spin=0, Reserved=00, KeyPhase=0, PN length=00)
		headerByte := byte(0x40) | byte(pnLen-1)

		// [header:1][dcid:8][pn:1][payload]
		// Short header has no length field — payload must be determinable from UDP datagram length
		// No random padding (would break decode without a length field)
		headerSize := 1 + 8 + pnLen // = 10 bytes

		wire = make([]byte, headerSize+encLen)
		off := 0

		wire[off] = headerByte
		off++

		copy(wire[off:off+8], dcid)
		off += 8

		// Packet number (1 byte)
		wire[off] = byte(pkt.SeqNum)
		off++

		// Encrypted payload (rest of UDP datagram)
		copy(wire[off:off+encLen], encPayload)
	}

	return wire, nil
}

// encodeVarint encodes a value as a QUIC variable-length integer (2 bytes, max 16383)
func encodeVarint(dst []byte, v uint64) {
	if v < 64 {
		dst[0] = byte(v)
		dst[1] = 0
	} else if v < 16384 {
		binary.BigEndian.PutUint16(dst, uint16(v)|0x4000)
	} else {
		// Fallback: truncate to 2-byte encoding
		binary.BigEndian.PutUint16(dst, uint16(v&0x3FFF)|0x4000)
	}
}

// decodeVarint decodes a QUIC variable-length integer, returns value and bytes consumed
func decodeVarint(data []byte) (uint64, int) {
	if len(data) < 1 {
		return 0, 0
	}
	prefix := data[0] >> 6
	switch prefix {
	case 0:
		return uint64(data[0] & 0x3F), 1
	case 1:
		if len(data) < 2 {
			return 0, 0
		}
		return uint64(binary.BigEndian.Uint16(data[:2]) & 0x3FFF), 2
	case 2:
		if len(data) < 4 {
			return 0, 0
		}
		return uint64(binary.BigEndian.Uint32(data[:4]) & 0x3FFFFFFF), 4
	default:
		if len(data) < 8 {
			return 0, 0
		}
		return binary.BigEndian.Uint64(data[:8]) & 0x3FFFFFFFFFFFFFFF, 8
	}
}

// Decode decrypts and deserializes wire bytes into a Packet.
// Supports both QUIC Long Header (Initial, for SYN/SYNACK) and Short Header (1-RTT, for everything else).
func (c *PacketCodec) Decode(wire []byte) (*Packet, error) {
	if len(wire) < 12 {
		return nil, fmt.Errorf("mtp: packet too short")
	}

	firstByte := wire[0]
	isLongHeader := (firstByte & 0x80) != 0 // bit 7 = Header Form

	c.dcidMu.RLock()
	dcid := append([]byte(nil), c.dcid...)
	c.dcidMu.RUnlock()

	var encPayload []byte
	var pnByte byte

	if isLongHeader {
		// QUIC Long Header (Initial)
		if (firstByte & 0x30) != 0x00 {
			// Not Initial type (bits 4-5 != 00) — skip for now, only handle Initial
			return nil, fmt.Errorf("mtp: unsupported long header type")
		}

		// [header:1][version:4][dcidLen:1][dcid:N][scidLen:1][scid:M][tokenLen:1][token:T][length:varint][pn:1..4][payload]
		off := 1

		// Skip Version (4 bytes)
		if len(wire) < off+4 {
			return nil, fmt.Errorf("mtp: short read version")
		}
		off += 4

		// DCID
		if len(wire) < off+1 {
			return nil, fmt.Errorf("mtp: short read dcid len")
		}
		dcidLen := int(wire[off])
		off++

		if len(wire) < off+dcidLen {
			return nil, fmt.Errorf("mtp: short read dcid")
		}
		// Verify DCID (constant-time)
		if dcidLen == 8 && !constantTimeCompare(wire[off:off+8], dcid) {
			return nil, fmt.Errorf("mtp: DCID mismatch (active probe?)")
		}
		off += dcidLen

		// SCID
		if len(wire) < off+1 {
			return nil, fmt.Errorf("mtp: short read scid len")
		}
		scidLen := int(wire[off])
		off++
		if len(wire) < off+scidLen {
			return nil, fmt.Errorf("mtp: short read scid")
		}
		off += scidLen

		// Token
		if len(wire) < off+1 {
			return nil, fmt.Errorf("mtp: short read token len")
		}
		tokenLen := int(wire[off])
		off++
		if len(wire) < off+tokenLen {
			return nil, fmt.Errorf("mtp: short read token")
		}
		off += tokenLen

		// Length (varint)
		payloadLen, varintSize := decodeVarint(wire[off:])
		if varintSize == 0 {
			return nil, fmt.Errorf("mtp: invalid varint length")
		}
		off += varintSize

		// Packet number (1 byte)
		if len(wire) < off+1 {
			return nil, fmt.Errorf("mtp: short read pn")
		}
		pnByte = wire[off]
		off++

		// Encrypted payload (varint includes pnLen, so subtract it)
		encPayloadLen := int(payloadLen) - 1
		if encPayloadLen < 0 || len(wire) < off+encPayloadLen {
			return nil, fmt.Errorf("mtp: short read payload (need %d, have %d)", encPayloadLen, len(wire)-off)
		}
		encPayload = wire[off : off+encPayloadLen]
		_ = pnByte // packet number available if needed

	} else {
		// QUIC Short Header (1-RTT)
		// [header:1][dcid:8][pn:1][payload]
		off := 1

		if len(wire) < off+8 {
			return nil, fmt.Errorf("mtp: short read dcid")
		}
		// Verify DCID (constant-time)
		if !constantTimeCompare(wire[off:off+8], dcid) {
			return nil, fmt.Errorf("mtp: DCID mismatch (active probe?)")
		}
		off += 8

		// Packet number (1 byte)
		if len(wire) < off+1 {
			return nil, fmt.Errorf("mtp: short read pn")
		}
		pnByte = wire[off]
		off++

		// Everything after is encrypted payload
		if len(wire) <= off {
			return nil, fmt.Errorf("mtp: no payload")
		}
		encPayload = wire[off:]
		_ = pnByte
	}

	// Decrypt: first 24 bytes are nonce, rest is ciphertext+tag
	aeadIface, err := c.getAEAD()
	if err != nil {
		return nil, fmt.Errorf("mtp: failed to create cipher: %w", err)
	}
	aead := aeadIface.(cipher.AEAD)

	nonceSize := aead.NonceSize()
	if len(encPayload) < nonceSize+aead.Overhead()+headerSize {
		return nil, fmt.Errorf("mtp: encrypted payload too short")
	}

	nonce := encPayload[:nonceSize]
	ciphertext := encPayload[nonceSize:]

	plaintextBuf := decodeBufferPool.Get().([]byte)
	defer decodeBufferPool.Put(plaintextBuf)

	plaintext, err := aead.Open(plaintextBuf[:0], nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("mtp: decryption failed: %w", err)
	}

	if len(plaintext) < headerSize {
		return nil, fmt.Errorf("mtp: plaintext too short for header")
	}

	// Parse MTP header
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

	rawPayload := make([]byte, payloadLen)
	copy(rawPayload, remaining[:payloadLen])

	if pkt.Flags&FlagCompressed != 0 {
		decompressed, err := c.decompressPayload(rawPayload)
		if err != nil {
			return nil, fmt.Errorf("mtp: decompression failed: %w", err)
		}
		pkt.Payload = decompressed
		pkt.Flags &^= FlagCompressed
	} else {
		pkt.Payload = rawPayload
	}

	// Parse SACK blocks if present
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
