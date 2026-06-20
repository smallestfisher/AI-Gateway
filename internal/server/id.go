package server

import "crypto/rand"

// randHex returns n random lowercase hex characters.
func randHex(n int) string {
	b := make([]byte, (n+1)/2)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand should not fail in practice; fall back to zeros
		return "000000000000"
	}
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
