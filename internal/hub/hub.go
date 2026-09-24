package hub

import "sync"

type Hub struct {
	mu      sync.Mutex
	waiters map[string]map[chan struct{}]struct{}
}

func New() *Hub {
	return &Hub{waiters: make(map[string]map[chan struct{}]struct{})}
}

func (h *Hub) Subscribe(ws string) (<-chan struct{}, func()) {
	ch := make(chan struct{}, 1)

	h.mu.Lock()
	if h.waiters[ws] == nil {
		h.waiters[ws] = make(map[chan struct{}]struct{})
	}
	h.waiters[ws][ch] = struct{}{}
	h.mu.Unlock()

	return ch, func() {
		h.mu.Lock()
		defer h.mu.Unlock()

		delete(h.waiters[ws], ch)
		if len(h.waiters[ws]) == 0 {
			delete(h.waiters, ws)
		}
	}
}

func (h *Hub) Publish(ws string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	for ch := range h.waiters[ws] {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}
