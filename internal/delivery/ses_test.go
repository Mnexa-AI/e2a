package delivery

import "testing"

func TestParseSESNotification(t *testing.T) {
	t.Run("delivery", func(t *testing.T) {
		ev, err := ParseSESNotification([]byte(`{
			"eventType":"Delivery",
			"mail":{"messageId":"ses-1","destination":["a@x.com"]},
			"delivery":{"recipients":["a@x.com"]}
		}`))
		if err != nil {
			t.Fatal(err)
		}
		if ev.Kind != KindDelivery || ev.SESMessageID != "ses-1" {
			t.Fatalf("got kind=%s id=%s", ev.Kind, ev.SESMessageID)
		}
		if len(ev.Recipients) != 1 || ev.Recipients[0].Status != StatusDelivered || ev.Recipients[0].Suppress {
			t.Fatalf("recipients=%+v", ev.Recipients)
		}
	})

	t.Run("permanent bounce suppresses", func(t *testing.T) {
		ev, _ := ParseSESNotification([]byte(`{
			"eventType":"Bounce",
			"mail":{"messageId":"ses-2"},
			"bounce":{"bounceType":"Permanent","bouncedRecipients":[{"emailAddress":"B@x.com","diagnosticCode":"550 no such user"}]}
		}`))
		if ev.Kind != KindBounce || len(ev.Recipients) != 1 {
			t.Fatalf("ev=%+v", ev)
		}
		r := ev.Recipients[0]
		if r.Address != "b@x.com" || r.Status != StatusBounced || !r.Suppress || r.Detail != "550 no such user" {
			t.Fatalf("recipient=%+v (address must be lowercased, suppress=true)", r)
		}
	})

	t.Run("transient bounce does NOT suppress", func(t *testing.T) {
		ev, _ := ParseSESNotification([]byte(`{
			"eventType":"Bounce",
			"mail":{"messageId":"ses-3"},
			"bounce":{"bounceType":"Transient","bouncedRecipients":[{"emailAddress":"c@x.com"}]}
		}`))
		if ev.Recipients[0].Suppress {
			t.Fatal("a transient (soft) bounce must not auto-suppress")
		}
		if ev.Recipients[0].Status != StatusBounced {
			t.Fatalf("status=%s", ev.Recipients[0].Status)
		}
	})

	t.Run("complaint suppresses", func(t *testing.T) {
		ev, _ := ParseSESNotification([]byte(`{
			"eventType":"Complaint",
			"mail":{"messageId":"ses-4"},
			"complaint":{"complainedRecipients":[{"emailAddress":"d@x.com"}],"complaintFeedbackType":"abuse"}
		}`))
		r := ev.Recipients[0]
		if ev.Kind != KindComplaint || r.Status != StatusComplained || !r.Suppress || r.Detail != "abuse" {
			t.Fatalf("recipient=%+v", r)
		}
	})

	t.Run("delivery delay → deferred, no suppress", func(t *testing.T) {
		ev, _ := ParseSESNotification([]byte(`{
			"eventType":"DeliveryDelay",
			"mail":{"messageId":"ses-5"},
			"deliveryDelay":{"delayedRecipients":[{"emailAddress":"e@x.com"}]}
		}`))
		if ev.Recipients[0].Status != StatusDeferred || ev.Recipients[0].Suppress {
			t.Fatalf("recipient=%+v", ev.Recipients[0])
		}
	})

	t.Run("legacy notificationType key", func(t *testing.T) {
		ev, err := ParseSESNotification([]byte(`{
			"notificationType":"Delivery",
			"mail":{"messageId":"ses-6","destination":["f@x.com"]}
		}`))
		if err != nil || ev.Kind != KindDelivery {
			t.Fatalf("ev=%+v err=%v", ev, err)
		}
	})

	t.Run("missing message id errors", func(t *testing.T) {
		if _, err := ParseSESNotification([]byte(`{"eventType":"Delivery","mail":{}}`)); err == nil {
			t.Fatal("expected error for missing mail.messageId")
		}
	})

	t.Run("unknown kind → Other, no recipients", func(t *testing.T) {
		ev, err := ParseSESNotification([]byte(`{"eventType":"Open","mail":{"messageId":"ses-7"}}`))
		if err != nil || ev.Kind != KindOther || len(ev.Recipients) != 0 {
			t.Fatalf("ev=%+v err=%v", ev, err)
		}
	})

	t.Run("malformed json errors", func(t *testing.T) {
		if _, err := ParseSESNotification([]byte(`{not json`)); err == nil {
			t.Fatal("expected error")
		}
	})
}
