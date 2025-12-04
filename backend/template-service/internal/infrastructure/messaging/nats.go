package messaging

import (
	"encoding/json"
	"template-service/internal/domain"
	"template-service/pkg/config"

	"github.com/nats-io/nats.go"
)

const serviceName = "template-service"

type NATSEventPublisher struct {
	Conn       *nats.Conn
	NatsConfig config.NatsConfig
}

func NewNATSEventPublisher(cfg config.NatsConfig) (*NATSEventPublisher, error) {
	conn, err := nats.Connect(cfg.URL)
	if err != nil {
		return nil, err
	}
	return &NATSEventPublisher{Conn: conn, NatsConfig: cfg}, nil
}

func (n *NATSEventPublisher) PublishDemoEvent(event domain.DemoEvent, headers map[string]string) error {
	js, err := n.Conn.JetStream()
	if err != nil {
		return err
	}
	data, _ := json.Marshal(event)
	msg := &nats.Msg{Subject: n.NatsConfig.DemoSubject, Data: data, Header: nats.Header{}}
	for k, v := range headers {
		msg.Header.Set(k, v)
	}
	_, err = js.PublishMsg(msg)
	return err
}

// EventSubscriber реализация для NATS
func (n *NATSEventPublisher) Subscribe(subject string, handler func(data []byte, headers map[string]string) error) error {
	js, err := n.Conn.JetStream()
	if err != nil {
		return err
	}
	_, err = js.QueueSubscribe(subject, serviceName, func(msg *nats.Msg) {
		headers := make(map[string]string)
		for k, v := range msg.Header {
			headers[k] = v[0]
		}
		handler(msg.Data, headers)
	}, nats.Durable(serviceName), nats.ManualAck())
	return err
}

// EnsureStream создает JetStream stream с нужным subject
func (n *NATSEventPublisher) EnsureStream(streamName string, subjects []string) error {
	js, err := n.Conn.JetStream()
	if err != nil {
		return err
	}
	_, err = js.AddStream(&nats.StreamConfig{
		Name:     streamName,
		Subjects: subjects,
		Storage:  nats.FileStorage,
	})
	if err != nil && err != nats.ErrStreamNameAlreadyInUse {
		return err
	}
	return nil
}
