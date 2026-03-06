package mtp

import (
	"bytes"
	"testing"
)

func TestPacketRoundtrip(t *testing.T) {
	codec := NewPacketCodec("test-secret-uuid-12345")

	tests := []struct {
		name string
		pkt  Packet
	}{
		{
			name: "DATA packet",
			pkt: Packet{
				Type:    PacketDATA,
				SeqNum:  42,
				AckNum:  10,
				Flags:   FlagNone,
				Payload: []byte("Hello, MTP world!"),
			},
		},
		{
			name: "ACK packet with SACK",
			pkt: Packet{
				Type:       PacketACK,
				SeqNum:     0,
				AckNum:     100,
				Flags:      FlagSACK,
				Payload:    []byte{},
				SACKBlocks: []uint32{102, 105, 110},
			},
		},
		{
			name: "SYN packet",
			pkt: Packet{
				Type:    PacketSYN,
				SeqNum:  0,
				Payload: []byte("AUTH:550e8400-e29b-41d4-a716-446655440000"),
			},
		},
		{
			name: "Empty payload",
			pkt: Packet{
				Type:    PacketPING,
				SeqNum:  999,
				Payload: []byte{},
			},
		},
		{
			name: "Max payload",
			pkt: Packet{
				Type:    PacketDATA,
				SeqNum:  1,
				Payload: bytes.Repeat([]byte{0xAB}, MaxPayloadSize),
			},
		},
		{
			name: "Migrate flag",
			pkt: Packet{
				Type:    PacketSYN,
				SeqNum:  0,
				Flags:   FlagMigrate,
				Payload: []byte("MIGRATE:some-session-id"),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Encode
			encoded, err := codec.Encode(&tt.pkt)
			if err != nil {
				t.Fatalf("Encode failed: %v", err)
			}

			t.Logf("Encoded size: %d bytes (payload: %d bytes)", len(encoded), len(tt.pkt.Payload))

			// Decode
			decoded, err := codec.Decode(encoded)
			if err != nil {
				t.Fatalf("Decode failed: %v", err)
			}

			// Verify
			if decoded.Type != tt.pkt.Type {
				t.Errorf("Type mismatch: got %d, want %d", decoded.Type, tt.pkt.Type)
			}
			if decoded.SeqNum != tt.pkt.SeqNum {
				t.Errorf("SeqNum mismatch: got %d, want %d", decoded.SeqNum, tt.pkt.SeqNum)
			}
			if decoded.AckNum != tt.pkt.AckNum {
				t.Errorf("AckNum mismatch: got %d, want %d", decoded.AckNum, tt.pkt.AckNum)
			}
			if decoded.Flags != tt.pkt.Flags {
				t.Errorf("Flags mismatch: got %d, want %d", decoded.Flags, tt.pkt.Flags)
			}
			if !bytes.Equal(decoded.Payload, tt.pkt.Payload) {
				t.Errorf("Payload mismatch: got %d bytes, want %d bytes", len(decoded.Payload), len(tt.pkt.Payload))
			}

			// Verify SACK blocks
			if tt.pkt.Flags&FlagSACK != 0 {
				if len(decoded.SACKBlocks) != len(tt.pkt.SACKBlocks) {
					t.Errorf("SACKBlocks count mismatch: got %d, want %d", len(decoded.SACKBlocks), len(tt.pkt.SACKBlocks))
				} else {
					for i, seq := range tt.pkt.SACKBlocks {
						if decoded.SACKBlocks[i] != seq {
							t.Errorf("SACKBlocks[%d] mismatch: got %d, want %d", i, decoded.SACKBlocks[i], seq)
						}
					}
				}
			}
		})
	}
}

func TestPacketPolymorphism(t *testing.T) {
	codec := NewPacketCodec("polymorphic-test-key")

	pkt := &Packet{
		Type:    PacketDATA,
		SeqNum:  1,
		Payload: []byte("same payload"),
	}

	// Encode same packet multiple times — each should produce different wire bytes
	encodings := make([][]byte, 10)
	for i := 0; i < 10; i++ {
		encoded, err := codec.Encode(pkt)
		if err != nil {
			t.Fatalf("Encode %d failed: %v", i, err)
		}
		encodings[i] = encoded
	}

	// Check that all encodings are different (due to random nonce and padding)
	allSame := true
	for i := 1; i < len(encodings); i++ {
		if !bytes.Equal(encodings[0], encodings[i]) {
			allSame = false
			break
		}
	}

	if allSame {
		t.Error("All 10 encodings of the same packet are identical — polymorphism is NOT working!")
	}

	// Verify all decode back to same content
	for i, encoded := range encodings {
		decoded, err := codec.Decode(encoded)
		if err != nil {
			t.Fatalf("Decode %d failed: %v", i, err)
		}
		if !bytes.Equal(decoded.Payload, pkt.Payload) {
			t.Errorf("Decode %d produced different payload", i)
		}
	}

	t.Logf("Polymorphism verified: %d different wire encodings for same packet", len(encodings))
}

func TestWrongKeyDecryption(t *testing.T) {
	codec1 := NewPacketCodec("correct-key")
	codec2 := NewPacketCodec("wrong-key")

	pkt := &Packet{
		Type:    PacketDATA,
		SeqNum:  1,
		Payload: []byte("secret data"),
	}

	encoded, err := codec1.Encode(pkt)
	if err != nil {
		t.Fatalf("Encode failed: %v", err)
	}

	// Trying to decode with wrong key should fail
	_, err = codec2.Decode(encoded)
	if err == nil {
		t.Error("Expected decryption failure with wrong key, but got nil error")
	}
}

// TestPaddingLength was removed because logic is now fully dynamic and inlined within Encode

func TestARQDeliveryOrdering(t *testing.T) {
	codec := NewPacketCodec("arq-test-key")

	var sent [][]byte
	sendFunc := func(data []byte) error {
		sent = append(sent, data)
		return nil
	}

	arq := NewARQEngine(codec, sendFunc, 64)
	defer arq.Close()

	// Simulate receiving DATA packets out of order
	packets := []*Packet{
		{Type: PacketDATA, SeqNum: 2, Payload: []byte("third")},
		{Type: PacketDATA, SeqNum: 0, Payload: []byte("first")},
		{Type: PacketDATA, SeqNum: 1, Payload: []byte("second")},
	}

	for _, pkt := range packets {
		arq.HandlePacket(pkt)
	}

	// Should deliver in order: first, second, third
	expected := []string{"first", "second", "third"}
	for i, exp := range expected {
		select {
		case pkt := <-arq.Delivered():
			if string(pkt.Payload) != exp {
				t.Errorf("Packet %d: got %q, want %q", i, string(pkt.Payload), exp)
			}
		default:
			t.Errorf("Packet %d: delivery channel empty, expected %q", i, exp)
		}
	}
}
