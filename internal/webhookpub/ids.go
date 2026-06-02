package webhookpub

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

// randHex16 returns 16 random bytes hex-encoded. Used for delivery
// row IDs (whd_<32-hex>). Panics on OS RNG failure for the same
// reason generateEventID does: an all-zero ID would collide across
// firings.
func randHex16() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("webhookpub: crypto/rand failed: %v", err))
	}
	return hex.EncodeToString(b)
}
