package webhook

import (
	"encoding/json"
	"errors"
)

// StripeEvent is the envelope of a Stripe event; Data.Object is the
// object the event is about, decoded by the app into its own type.
type StripeEvent struct {
	ID       string `json:"id"`
	Type     string `json:"type"` // "checkout.session.completed"
	Created  int64  `json:"created"`
	Livemode bool   `json:"livemode"`
	Data     struct {
		Object json.RawMessage `json:"object"`
	} `json:"data"`
}

// StripeEvent decodes the verified body as a Stripe event.
func (d *Delivery) StripeEvent() (StripeEvent, error) {
	var ev StripeEvent
	if err := d.Decode(&ev); err != nil {
		return ev, err
	}
	if ev.Type == "" {
		return ev, errors.New("webhook: not a Stripe event (no type)")
	}
	return ev, nil
}

// MailgunEvent is the "event-data" of a Mailgun event webhook; Raw holds
// all of it for the fields not listed.
type MailgunEvent struct {
	Event     string  `json:"event"` // "delivered", "failed", "opened"
	ID        string  `json:"id"`
	Timestamp float64 `json:"timestamp"`
	Recipient string  `json:"recipient"`
	Severity  string  `json:"severity"` // "permanent" or "temporary" for "failed"
	Reason    string  `json:"reason"`
	Message   struct {
		Headers struct {
			MessageID string `json:"message-id"`
		} `json:"headers"`
	} `json:"message"`
	Raw json.RawMessage `json:"-"`
}

// MailgunEvent decodes the "event-data" of a Mailgun JSON webhook. Form
// posts (inbound routes) have none: read Form instead.
func (d *Delivery) MailgunEvent() (MailgunEvent, error) {
	var env struct {
		Data json.RawMessage `json:"event-data"`
	}
	var ev MailgunEvent
	if d.Form != nil {
		return ev, errors.New("webhook: a Mailgun form post has no event-data; read Delivery.Form")
	}
	if err := d.Decode(&env); err != nil {
		return ev, err
	}
	if len(env.Data) == 0 {
		return ev, errors.New("webhook: no event-data in the Mailgun body")
	}
	if err := json.Unmarshal(env.Data, &ev); err != nil {
		return ev, err
	}
	ev.Raw = env.Data
	return ev, nil
}
