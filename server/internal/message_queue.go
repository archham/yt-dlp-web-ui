package internal

import (
	"context"
	"errors"
	"log/slog"

	evbus "github.com/asaskevich/EventBus"
	"github.com/marcopiovanello/yt-dlp-web-ui/v3/server/config"
	"golang.org/x/sync/semaphore"
)

const queueName = "process:pending"

type MessageQueue struct {
	concurrency int
	eventBus    evbus.Bus
}

// Creates a new message queue.
// By default it will be created with a size equals to nthe number of logical
// CPU cores -1.
// The queue size can be set via the qs flag.
func NewMessageQueue() (*MessageQueue, error) {
	qs := config.Instance().QueueSize

	if qs <= 0 {
		return nil, errors.New("invalid queue size")
	}

	return &MessageQueue{
		concurrency: qs,
		eventBus:    evbus.New(),
	}, nil
}

// Publish a message to the queue and set the task to a peding state.
func (m *MessageQueue) Publish(p *Process) {
	// needs to have an id set before
	p.SetPending()

	m.eventBus.Publish(queueName, p)
}

func (m *MessageQueue) SetupConsumers() {
	go m.downloadConsumer()
	go m.metadataSubscriber()
}

// Setup the consumer listener which subscribes to the changes to the producer
// channel and triggers the "download" action.
func (m *MessageQueue) downloadConsumer() {
	sem := semaphore.NewWeighted(int64(m.concurrency))

	m.eventBus.SubscribeAsync(queueName, func(p *Process) {
		sem.Acquire(context.Background(), 1)
		defer sem.Release(1)

		slog.Info("received process from event bus",
			slog.String("bus", queueName),
			slog.String("consumer", "downloadConsumer"),
			slog.String("id", p.getShortId()),
		)

		if p.Progress.Status != StatusCompleted {
			slog.Info("started process",
				slog.String("bus", queueName),
				slog.String("id", p.getShortId()),
			)
			if p.Livestream {
				// livestreams have higher priorty and they ignore the semaphore
				go p.Start()
			} else {
				p.Start()
			}
		}
	}, false)
}

// Setup the metadata consumer listener which subscribes to the changes to the
// producer channel and adds metadata to each download.
func (m *MessageQueue) metadataSubscriber() {
	// How many concurrent metadata fetcher jobs are spawned
	// Since there's ongoing downloads, 1 job at time seems a good compromise
	sem := semaphore.NewWeighted(1)

	m.eventBus.SubscribeAsync(queueName, func(p *Process) {
		sem.Acquire(context.Background(), 1)
		defer sem.Release(1)

		slog.Info("received process from event bus",
			slog.String("bus", queueName),
			slog.String("consumer", "metadataConsumer"),
			slog.String("id", p.getShortId()),
		)

		if p.Progress.Status == StatusCompleted {
			slog.Warn("proccess has an illegal state",
				slog.String("id", p.getShortId()),
				slog.Int("status", p.Progress.Status),
			)
			return
		}

		if err := p.SetMetadata(); err != nil {
			slog.Error("failed to retrieve metadata",
				slog.String("id", p.getShortId()),
				slog.String("err", err.Error()),
			)
		}
	}, false)
}
