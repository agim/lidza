package realtime

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/valkey-io/valkey-go"
)

// channel is the pub/sub channel every node shares.
const channel = "lidza:realtime"

// ValkeyBus fans messages out through a Valkey (or Redis) server.
type ValkeyBus struct {
	client valkey.Client
}

// NewValkeyBus connects to url (redis://host:port or valkey://...).
func NewValkeyBus(ctx context.Context, url string) (*ValkeyBus, error) {
	opt, err := valkey.ParseURL(url)
	if err != nil {
		return nil, fmt.Errorf("REALTIME_BUS_URL: %w", err)
	}
	client, err := valkey.NewClient(opt)
	if err != nil {
		return nil, fmt.Errorf("realtime bus: %w", err)
	}
	if err := client.Do(ctx, client.B().Ping().Build()).Error(); err != nil {
		client.Close()
		return nil, fmt.Errorf("realtime bus not reachable: %w", err)
	}
	return &ValkeyBus{client: client}, nil
}

type envelope struct {
	Topic string          `json:"t"`
	Data  json.RawMessage `json:"d"`
}

// Publish implements Bus.
func (b *ValkeyBus) Publish(ctx context.Context, topic string, data []byte) error {
	payload, err := json.Marshal(envelope{Topic: topic, Data: data})
	if err != nil {
		return err
	}
	return b.client.Do(ctx, b.client.B().Publish().Channel(channel).Message(string(payload)).Build()).Error()
}

// Subscribe implements Bus; it blocks until ctx ends.
func (b *ValkeyBus) Subscribe(ctx context.Context, deliver func(topic string, data []byte)) error {
	return b.client.Receive(ctx, b.client.B().Subscribe().Channel(channel).Build(), func(msg valkey.PubSubMessage) {
		var e envelope
		if json.Unmarshal([]byte(msg.Message), &e) == nil {
			deliver(e.Topic, e.Data)
		}
	})
}

// Close implements Bus.
func (b *ValkeyBus) Close() error {
	b.client.Close()
	return nil
}
