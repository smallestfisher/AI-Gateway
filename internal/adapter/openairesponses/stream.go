package openairesponses

import (
	crand "crypto/rand"

	"github.com/aigateway/ai-hub/internal/ir"
)

// randHex returns n lowercase hex chars.
func randHex(n int) string {
	b := make([]byte, (n+1)/2)
	_, _ = crand.Read(b)
	const hexd = "0123456789abcdef"
	out := make([]byte, n)
	for i := 0; i < n; i++ {
		v := b[i/2]
		if i%2 == 0 {
			out[i] = hexd[v>>4]
		} else {
			out[i] = hexd[v&0x0f]
		}
	}
	return string(out)
}

// Streaming stubs — the Responses streaming protocol is large and lands in a
// follow-up. Non-streaming conversion is fully supported above.

type streamEnc struct{}

func (s *streamEnc) Encode(ir.StreamEvent) ([]byte, error) { return nil, errStreamingNotImpl }
func (s *streamEnc) Flush() ([]byte, error)                { return nil, errStreamingNotImpl }

type streamDec struct{}

func (s *streamDec) Feed([]byte) ([]ir.StreamEvent, error) { return nil, errStreamingNotImpl }
func (s *streamDec) Finalize() ([]ir.StreamEvent, error)   { return nil, errStreamingNotImpl }
