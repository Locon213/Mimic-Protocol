package mtp

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"fmt"

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
	FlagNone     uint8 = 0x00
	FlagMigrate  uint8 = 0x01 // Session migration
	FlagSACK     uint8 = 0x02 // Selective ACK
	FlagFragment uint8 = 0x04 // Fragmented payload
)

// MaxPayloadSize is the max payload per MTP packet (safe UDP MTU)
const MaxPayloadSize = 1200

// headerSize is the fixed size of the plaintext header before encryption
// Type(1) + SeqNum(4) + AckNum(4) + PayloadLen(2) + Flags(1) = 12 bytes
const headerSize = 12

// Packet represents a decoded MTP packet
type Packet struct {
	Type       uint8
	SeqNum     uint32
	AckNum     uint32
	Flags      uint8
	Payload    []byte
	SACKBlocks []uint32 // For selective ACK: list of received sequence numbers
}

// PacketCodec handles polymorphic encoding and decoding of MTP packets
type PacketCodec struct {
	sharedKey []byte // 32-byte key derived from UUID
	dcid      []byte // 8-byte Connection ID for QUIC masking
}

// NewPacketCodec creates a new codec with the given shared secret
func NewPacketCodec(secret string) *PacketCodec {
	key := deriveKey(secret)

	// Derive an 8-byte DCID from the key
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte("QUIC_DCID_DERIVATION"))
	dcid := mac.Sum(nil)[:8]

	return &PacketCodec{sharedKey: key, dcid: dcid}
}

// deriveKey creates a 32-byte key from a string secret
func deriveKey(secret string) []byte {
	hash := sha256.Sum256([]byte(secret))
	return hash[:]
}

// Функция paddingLength удалена, генерация случайной длины будет встроена напрямую в Encode

// Encode serializes and encrypts a Packet into wire format:
// [ random junk padding ] [ nonce ] [ encrypted(header + payload) ]
func (c *PacketCodec) Encode(pkt *Packet) ([]byte, error) {
	// 1. Build plaintext header
	header := make([]byte, headerSize)
	header[0] = pkt.Type
	binary.BigEndian.PutUint32(header[1:5], pkt.SeqNum)
	binary.BigEndian.PutUint32(header[5:9], pkt.AckNum)
	binary.BigEndian.PutUint16(header[9:11], uint16(len(pkt.Payload)))
	header[11] = pkt.Flags

	// 2. Build plaintext: header + payload + optional SACK blocks
	plaintext := append(header, pkt.Payload...)

	if pkt.Flags&FlagSACK != 0 && len(pkt.SACKBlocks) > 0 {
		sackData := make([]byte, 2+len(pkt.SACKBlocks)*4)
		binary.BigEndian.PutUint16(sackData[0:2], uint16(len(pkt.SACKBlocks)))
		for i, seq := range pkt.SACKBlocks {
			binary.BigEndian.PutUint32(sackData[2+i*4:], seq)
		}
		plaintext = append(plaintext, sackData...)
	}

	// 3. Encrypt with ChaCha20-Poly1305
	aead, err := chacha20poly1305.NewX(c.sharedKey)
	if err != nil {
		return nil, fmt.Errorf("mtp: failed to create cipher: %w", err)
	}

	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("mtp: failed to generate nonce: %w", err)
	}

	ciphertext := aead.Seal(nil, nonce, plaintext, nil)

	// 4. Generate smart junk padding
	// Max safe UDP payload size is ~1350 bytes.
	baseSize := 2 + len(nonce) + len(ciphertext)
	maxPad := 1350 - baseSize
	if maxPad < 10 {
		maxPad = 10
	}

	randBytes := make([]byte, 2)
	rand.Read(randBytes)
	rng := binary.BigEndian.Uint16(randBytes)

	// 50% chance to pad up to MTU, 50% chance for small padding
	var padLen int
	if rng%2 == 0 {
		padLen = int(rng%uint16(maxPad)) + 1
	} else {
		padLen = int(rng%32) + 1
	}

	padding := make([]byte, padLen)
	rand.Read(padding)

	// 5. Wire format with QUIC masking:
	// [QUIC Short Header: 9 bytes] [padLen:2] [padding:padLen] [nonce:24] [ciphertext:N]
	quicHeader := make([]byte, 9)
	quicHeader[0] = 0x40 // QUIC Short Header flag (0x40 = 0b01000000)
	copy(quicHeader[1:], c.dcid)

	wire := make([]byte, 0, 9+2+padLen+len(nonce)+len(ciphertext))
	padHeader := make([]byte, 2)
	binary.BigEndian.PutUint16(padHeader, uint16(padLen))

	wire = append(wire, quicHeader...)
	wire = append(wire, padHeader...)
	wire = append(wire, padding...)
	wire = append(wire, nonce...)
	wire = append(wire, ciphertext...)

	return wire, nil
}

// Decode decrypts and deserializes wire bytes into a Packet.
// It uses the seqNum-hint from the padding length derivation to strip junk.
func (c *PacketCodec) Decode(wire []byte) (*Packet, error) {
	if len(wire) < 9+3 {
		return nil, fmt.Errorf("mtp: packet too short")
	}

	// 1. Verify QUIC Short Header
	if wire[0] != 0x40 {
		return nil, fmt.Errorf("mtp: invalid QUIC short header flag")
	}
	if !bytes.Equal(wire[1:9], c.dcid) {
		return nil, fmt.Errorf("mtp: DCID mismatch (active probe?)")
	}

	offset := 9 // Skip QUIC header

	// 2. Read padding length (2 bytes)
	padLen := int(binary.BigEndian.Uint16(wire[offset : offset+2]))
	if padLen < 1 || padLen > 1350 {
		return nil, fmt.Errorf("mtp: invalid padding length %d", padLen)
	}

	aead, err := chacha20poly1305.NewX(c.sharedKey)
	if err != nil {
		return nil, fmt.Errorf("mtp: failed to create cipher: %w", err)
	}

	nonceSize := aead.NonceSize()
	minLen := offset + 2 + padLen + nonceSize + aead.Overhead() + headerSize
	if len(wire) < minLen {
		return nil, fmt.Errorf("mtp: packet too short for declared padding")
	}

	// 3. Skip padding, extract nonce and ciphertext
	dataOffset := offset + 2 + padLen
	nonce := wire[dataOffset : dataOffset+nonceSize]
	ciphertext := wire[dataOffset+nonceSize:]

	// 3. Decrypt
	plaintext, err := aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("mtp: decryption failed: %w", err)
	}

	if len(plaintext) < headerSize {
		return nil, fmt.Errorf("mtp: plaintext too short for header")
	}

	// 4. Parse header
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

	pkt.Payload = remaining[:payloadLen]

	// 5. Parse SACK blocks if present
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
