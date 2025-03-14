package events

import (
	"sync"
)

// EventType represents the type of event
type EventType string

// Event types
const (
	AppDeployed     EventType = "app_deployed"
	AppStarted      EventType = "app_started"
	AppStopped      EventType = "app_stopped"
	AppFailed       EventType = "app_failed"
	BackupCompleted EventType = "backup_completed"
	BackupFailed    EventType = "backup_failed"
	ProxyConfigured EventType = "proxy_configured"
)

// Event represents a system event
type Event struct {
	Type    EventType
	AppID   string
	Message string
	Data    map[string]interface{}
}

// Handler is a function that handles an event
type Handler func(Event)

// EventBus is a simple pub/sub implementation
type EventBus struct {
	subscribers map[EventType][]Handler
	mu          sync.RWMutex
}

// NewEventBus creates a new event bus
func NewEventBus() *EventBus {
	return &EventBus{
		subscribers: make(map[EventType][]Handler),
	}
}

// Subscribe registers a handler for a specific event type
func (b *EventBus) Subscribe(eventType EventType, handler Handler) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.subscribers[eventType] = append(b.subscribers[eventType], handler)
}

// Publish sends an event to all subscribers
func (b *EventBus) Publish(event Event) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if handlers, ok := b.subscribers[event.Type]; ok {
		for _, handler := range handlers {
			go handler(event)
		}
	}
}

// Clear removes all subscribers
func (b *EventBus) Clear() {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.subscribers = make(map[EventType][]Handler)
}
