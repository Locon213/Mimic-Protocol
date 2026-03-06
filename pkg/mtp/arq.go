package mtp

import (
	"fmt"
	"math"
	"sync"
	"time"
)

// ARQ engine constants
const (
	DefaultWindowSize     = 64
	MaxWindowSize         = 256
	InitialRTO            = 500 * time.Millisecond
	MinRTO                = 100 * time.Millisecond
	MaxRTO                = 10 * time.Second
	MaxRetransmissions    = 10
	DuplicateACKThreshold = 3
)

type inflight struct {
	pkt        *Packet
	encoded    []byte
	sentAt     time.Time
	retries    int
	retransmit *time.Timer
	delivered  uint64 // Поле для расчетов BBR на момент отправки
}

// ARQEngine manages reliable delivery over unreliable UDP
type ARQEngine struct {
	mu sync.Mutex

	// Send state
	sendSeq    uint32               // Next sequence number to assign
	sendWindow int                  // Current congestion window size
	ssthresh   int                  // Slow start threshold
	unacked    map[uint32]*inflight // Packets waiting for ACK

	// Receive state
	recvNext  uint32             // Next expected sequence number
	recvBuf   map[uint32]*Packet // Out-of-order received packets
	delivered chan *Packet       // Ordered packets ready for Read()

	// RTT estimation (Jacobson/Karels algorithm)
	srtt    time.Duration // Smoothed RTT
	rttvar  time.Duration // RTT variance
	rto     time.Duration // Retransmission timeout
	rttInit bool          // Whether we've measured the first RTT

	// BBR (Bottleneck Bandwidth and Round-trip propagation time)
	minRTT         time.Duration
	btlBw          float64 // bytes per nanosecond
	bytesDelivered uint64  // total bytes delivered

	// Callbacks
	sendFunc func([]byte) error // Send raw bytes over UDP
	codec    *PacketCodec

	// Stats
	totalRetransmissions uint64
	totalPacketsSent     uint64
	totalPacketsRecv     uint64

	// FEC
	fecEnc *FecEncoder
	fecDec *FecDecoder

	// Lifecycle
	closed  bool
	closeCh chan struct{}
}

// NewARQEngine creates a new ARQ engine
func NewARQEngine(codec *PacketCodec, sendFunc func([]byte) error, deliverBufSize int) *ARQEngine {
	arq := &ARQEngine{
		sendWindow: 2, // Start with slow start
		ssthresh:   DefaultWindowSize,
		unacked:    make(map[uint32]*inflight),
		recvBuf:    make(map[uint32]*Packet),
		delivered:  make(chan *Packet, deliverBufSize),
		rto:        InitialRTO,
		sendFunc:   sendFunc,
		codec:      codec,
		closeCh:    make(chan struct{}),
		minRTT:     10 * time.Second,
	}

	enc, _ := NewFecEncoder(func(startSeq uint32, parityIdx uint8, payload []byte) {
		pkt := &Packet{
			Type:    PacketFEC,
			SeqNum:  startSeq,
			Flags:   parityIdx, // Reuse Flags to store parity index (0 or 1)
			Payload: payload,
			AckNum:  arq.recvNext, // Piggyback
		}
		if encoded, err := codec.Encode(pkt); err == nil {
			sendFunc(encoded)
		}
	})
	arq.fecEnc = enc

	dec, _ := NewFecDecoder(func(seq uint32, payload []byte) {
		pkt := &Packet{
			Type:    PacketDATA,
			SeqNum:  seq,
			Payload: append([]byte(nil), payload...), // Copy
		}
		// Dispatch back into ARQ
		go arq.HandlePacket(pkt)
	})
	arq.fecDec = dec

	return arq
}

// Send queues a DATA packet for reliable delivery.
// It blocks if the congestion window is full.
func (a *ARQEngine) Send(payload []byte) error {
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return fmt.Errorf("mtp: arq engine closed")
	}

	// Wait for window space
	for len(a.unacked) >= a.sendWindow {
		a.mu.Unlock()
		// Brief sleep to avoid busy loop; in production use a condition variable
		select {
		case <-a.closeCh:
			return fmt.Errorf("mtp: arq engine closed")
		case <-time.After(5 * time.Millisecond):
		}
		a.mu.Lock()
		if a.closed {
			a.mu.Unlock()
			return fmt.Errorf("mtp: arq engine closed")
		}
	}

	seq := a.sendSeq
	a.sendSeq++

	pkt := &Packet{
		Type:    PacketDATA,
		SeqNum:  seq,
		AckNum:  a.recvNext, // Piggyback our ACK
		Payload: payload,
	}

	encoded, err := a.codec.Encode(pkt)
	if err != nil {
		a.mu.Unlock()
		return fmt.Errorf("mtp: encode failed: %w", err)
	}

	inf := &inflight{
		pkt:       pkt,
		encoded:   encoded,
		sentAt:    time.Now(),
		delivered: a.bytesDelivered,
	}

	a.unacked[seq] = inf
	a.totalPacketsSent++
	a.mu.Unlock()

	// Add to Forward Error Correction encoder
	a.fecEnc.AddDataPacket(seq, payload)

	// Send the packet
	if err := a.sendFunc(encoded); err != nil {
		return err
	}

	// Start retransmission timer
	a.mu.Lock()
	if inf2, ok := a.unacked[seq]; ok {
		inf2.retransmit = time.AfterFunc(a.rto, func() {
			a.retransmitPacket(seq)
		})
	}
	a.mu.Unlock()

	return nil
}

// SendControl sends a control packet (SYN, ACK, FIN, etc.) without ARQ tracking
func (a *ARQEngine) SendControl(pkt *Packet) error {
	encoded, err := a.codec.Encode(pkt)
	if err != nil {
		return fmt.Errorf("mtp: encode control failed: %w", err)
	}
	return a.sendFunc(encoded)
}

// HandlePacket processes a received packet (called from the recv loop)
func (a *ARQEngine) HandlePacket(pkt *Packet) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.closed {
		return
	}

	a.totalPacketsRecv++

	switch pkt.Type {
	case PacketACK:
		a.handleACK(pkt)
	case PacketDATA:
		a.fecDec.AddData(pkt.SeqNum, pkt.Payload)
		a.handleDATA(pkt)
	case PacketFEC:
		a.fecDec.AddParity(pkt.SeqNum, pkt.Flags, append([]byte(nil), pkt.Payload...))
	}
}

// handleACK processes an incoming ACK
func (a *ARQEngine) handleACK(pkt *Packet) {
	ackNum := pkt.AckNum

	// Handle cumulative ACK: acknowledge all packets up to ackNum
	for seq, inf := range a.unacked {
		if seq < ackNum {
			a.processAckedPacket(seq, inf)
		}
	}

	// Handle selective ACK blocks
	if pkt.Flags&FlagSACK != 0 {
		for _, sackSeq := range pkt.SACKBlocks {
			if inf, ok := a.unacked[sackSeq]; ok {
				a.processAckedPacket(sackSeq, inf)
			}
		}
	}

	// Congestion control: BBR-like target window
	if a.minRTT > 0 && a.btlBw > 0 {
		// BDP (Bytes) = Bottleneck Bandwidth * Min RTT
		bdpBytes := a.btlBw * float64(a.minRTT.Nanoseconds())
		targetWindow := int((bdpBytes / 1000.0) * 1.5) // Gain = 1.5, avg packet = 1000 bytes

		if targetWindow < 4 {
			targetWindow = 4
		}
		if targetWindow > MaxWindowSize {
			targetWindow = MaxWindowSize
		}

		// Smooth adjustment
		if a.sendWindow < targetWindow {
			a.sendWindow++
		} else if a.sendWindow > targetWindow {
			a.sendWindow--
		}
	} else {
		// Fallback to slow start
		if a.sendWindow < a.ssthresh {
			a.sendWindow = min(a.sendWindow*2, MaxWindowSize)
		} else {
			a.sendWindow = min(a.sendWindow+1, MaxWindowSize)
		}
	}
}

func (a *ARQEngine) processAckedPacket(seq uint32, inf *inflight) {
	a.bytesDelivered += uint64(len(inf.pkt.Payload))

	if inf.retries == 0 {
		deliveryTime := time.Since(inf.sentAt)
		a.updateRTT(deliveryTime)

		if a.minRTT == 0 || deliveryTime < a.minRTT {
			a.minRTT = deliveryTime
		}

		if deliveryTime > 0 {
			bytesDelivered := a.bytesDelivered - inf.delivered
			rate := float64(bytesDelivered) / float64(deliveryTime.Nanoseconds())

			// Max filter / EMA for bandwidth
			if rate > a.btlBw {
				a.btlBw = rate
			} else {
				a.btlBw = a.btlBw*0.9 + rate*0.1
			}
		}
	}

	if inf.retransmit != nil {
		inf.retransmit.Stop()
	}
	delete(a.unacked, seq)
}

// handleDATA processes an incoming DATA packet
func (a *ARQEngine) handleDATA(pkt *Packet) {
	seq := pkt.SeqNum

	if seq == a.recvNext {
		// In-order delivery
		a.deliverPacket(pkt)
		a.recvNext++

		// Deliver any buffered packets that are now in order
		for {
			if buffered, ok := a.recvBuf[a.recvNext]; ok {
				a.deliverPacket(buffered)
				delete(a.recvBuf, a.recvNext)
				a.recvNext++
			} else {
				break
			}
		}
	} else if seq > a.recvNext {
		// Out-of-order: buffer it
		a.recvBuf[seq] = pkt
	}
	// else: duplicate, ignore

	// Send ACK (with SACK if we have out-of-order packets)
	a.sendACK()
}

// deliverPacket sends a packet to the delivery channel (non-blocking drop if full)
func (a *ARQEngine) deliverPacket(pkt *Packet) {
	select {
	case a.delivered <- pkt:
	default:
		// Buffer full, drop (shouldn't happen with well-sized buffer)
	}
}

// sendACK sends an ACK packet with optional SACK blocks
func (a *ARQEngine) sendACK() {
	ack := &Packet{
		Type:   PacketACK,
		SeqNum: 0,
		AckNum: a.recvNext,
	}

	// Add SACK blocks for out-of-order packets
	if len(a.recvBuf) > 0 {
		ack.Flags |= FlagSACK
		ack.SACKBlocks = make([]uint32, 0, len(a.recvBuf))
		for seq := range a.recvBuf {
			ack.SACKBlocks = append(ack.SACKBlocks, seq)
			if len(ack.SACKBlocks) >= 32 { // Limit SACK block count
				break
			}
		}
	}

	encoded, err := a.codec.Encode(ack)
	if err != nil {
		return
	}
	a.sendFunc(encoded) // Best effort
}

// retransmitPacket handles retransmission of a specific packet
func (a *ARQEngine) retransmitPacket(seq uint32) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.closed {
		return
	}

	inf, ok := a.unacked[seq]
	if !ok {
		return // Already ACKed
	}

	inf.retries++
	if inf.retries > MaxRetransmissions {
		// Give up on this packet — connection is likely dead
		delete(a.unacked, seq)
		return
	}

	// Congestion event: BBR limits window reduction
	// Instead of halving the window, we reduce it by 20%
	a.ssthresh = max(a.sendWindow*4/5, 4)
	a.sendWindow = a.ssthresh

	// Exponential backoff on RTO
	a.rto = time.Duration(math.Min(float64(a.rto*2), float64(MaxRTO)))

	a.totalRetransmissions++

	// Re-encode with fresh junk padding (polymorphic!)
	encoded, err := a.codec.Encode(inf.pkt)
	if err != nil {
		return
	}
	inf.encoded = encoded
	inf.sentAt = time.Now()

	// Send
	a.sendFunc(encoded)

	// Restart timer
	inf.retransmit = time.AfterFunc(a.rto, func() {
		a.retransmitPacket(seq)
	})
}

// updateRTT updates the smoothed RTT and RTO using Jacobson/Karels algorithm
func (a *ARQEngine) updateRTT(rtt time.Duration) {
	if !a.rttInit {
		a.srtt = rtt
		a.rttvar = rtt / 2
		a.rttInit = true
	} else {
		// SRTT = (1 - α) * SRTT + α * RTT   where α = 1/8
		a.srtt = a.srtt*7/8 + rtt/8
		// RTTVAR = (1 - β) * RTTVAR + β * |SRTT - RTT|   where β = 1/4
		diff := a.srtt - rtt
		if diff < 0 {
			diff = -diff
		}
		a.rttvar = a.rttvar*3/4 + diff/4
	}
	// RTO = SRTT + max(G, 4*RTTVAR)  where G = clock granularity (1ms)
	a.rto = a.srtt + 4*a.rttvar
	if a.rto < MinRTO {
		a.rto = MinRTO
	}
	if a.rto > MaxRTO {
		a.rto = MaxRTO
	}
}

// Delivered returns the channel for in-order delivered packets
func (a *ARQEngine) Delivered() <-chan *Packet {
	return a.delivered
}

// Close stops the ARQ engine and cancels all pending retransmissions
func (a *ARQEngine) Close() {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.closed {
		return
	}
	a.closed = true
	close(a.closeCh)

	for _, inf := range a.unacked {
		if inf.retransmit != nil {
			inf.retransmit.Stop()
		}
	}
	a.unacked = nil
}

// Stats returns current engine statistics
func (a *ARQEngine) Stats() (sent, recv, retransmissions uint64, window int, rto time.Duration) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.totalPacketsSent, a.totalPacketsRecv, a.totalRetransmissions, a.sendWindow, a.rto
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
