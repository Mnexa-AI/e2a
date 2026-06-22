package delivery_test

import (
	"testing"

	"github.com/Mnexa-AI/e2a/internal/delivery"
	"github.com/Mnexa-AI/e2a/internal/webhookpub"
)

// TestDeliveryEventConstantsInCatalog pins the delivery package's locally-defined
// emitter event strings (kept local to avoid a production dependency on
// webhookpub) to the canonical catalog. Without this, renaming a constant here
// would emit an event type no subscriber's enum accepts — an
// emitted-but-unsubscribable split, the producer-side twin of the drift the
// httpapi enum gate guards.
func TestDeliveryEventConstantsInCatalog(t *testing.T) {
	for _, et := range []string{
		delivery.EventEmailDelivered,
		delivery.EventEmailBounced,
		delivery.EventEmailComplained,
		delivery.EventSuppressionAdded,
	} {
		if !webhookpub.IsValidEventType(et) {
			t.Errorf("delivery emits %q which is not in webhookpub.AllEventTypes (emitted-but-unsubscribable)", et)
		}
	}
}
