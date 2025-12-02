package protocol

// Protocol defines the core interface for the Mimic protocol.
// It handles the obfuscation and transmission of data.
type Protocol interface {
	// Handshake establishes a secure connection with the server
	Handshake() error
	// Send transmits data with the current mimicry pattern
	Send(data []byte) (int, error)
	// Receive reads data from the connection
	Receive() ([]byte, error)
}
