package hub_test

import (
	"sync"
	"testing"
	"time"

	"github.com/eusean-tg/bro/internal/hub"
)

// woken reports whether ch holds a pending wake, without waiting. Publish
// sends into a buffered channel before it returns, so after Publish the wake
// is either there already or was never sent.
func woken(ch <-chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

func TestPublishWakesASubscriber(t *testing.T) {
	h := hub.New()
	ch, unsub := h.Subscribe("alpha")
	defer unsub()

	h.Publish("alpha")

	if !woken(ch) {
		t.Error("subscriber was not woken by a publish to its workspace")
	}
}

func TestPublishWakesEverySubscriberOfTheWorkspace(t *testing.T) {
	h := hub.New()
	var chans []<-chan struct{}
	for range 3 {
		ch, unsub := h.Subscribe("alpha")
		defer unsub()
		chans = append(chans, ch)
	}

	h.Publish("alpha")

	for i, ch := range chans {
		if !woken(ch) {
			t.Errorf("subscriber %d was not woken", i)
		}
	}
}

func TestPublishIsScopedToOneWorkspace(t *testing.T) {
	h := hub.New()
	ch, unsub := h.Subscribe("alpha")
	defer unsub()

	h.Publish("beta")

	if woken(ch) {
		t.Error("a publish to beta woke a subscriber of alpha")
	}
}

func TestPublishWithNoSubscribersIsHarmless(t *testing.T) {
	h := hub.New()
	h.Publish("nobody-here")
}

func TestPublishNeverBlocksOnAPendingWake(t *testing.T) {
	h := hub.New()
	ch, unsub := h.Subscribe("alpha")

	// The subscriber never reads. If Publish blocked on a full channel, the
	// second call would hang forever, holding the hub's lock; unsub is
	// called at the end rather than deferred so a failure here can report
	// instead of deadlocking on that lock.
	publish100 := func() {
		for range 100 {
			h.Publish("alpha")
		}
	}
	if !within(2*time.Second, publish100) {
		t.Fatal("Publish blocked on a subscriber that already had a pending wake")
	}

	// Many publishes collapse into one pending wake.
	if !woken(ch) {
		t.Fatal("no wake pending after 100 publishes")
	}
	if woken(ch) {
		t.Error("more than one wake pending; the buffer should hold exactly one")
	}
	unsub()
}

func TestUnsubscribedChannelsAreNotWoken(t *testing.T) {
	h := hub.New()
	ch, unsub := h.Subscribe("alpha")
	unsub()

	h.Publish("alpha")

	if woken(ch) {
		t.Error("a channel was woken after unsubscribing")
	}
}

func TestUnsubscribeRemovesOnlyItsOwnChannel(t *testing.T) {
	h := hub.New()
	gone, unsubGone := h.Subscribe("alpha")
	kept, unsubKept := h.Subscribe("alpha")
	defer unsubKept()
	unsubGone()

	h.Publish("alpha")

	if woken(gone) {
		t.Error("the unsubscribed channel was woken")
	}
	if !woken(kept) {
		t.Error("unsubscribing one channel stopped wakes to another")
	}
}

func TestUnsubscribeTwiceIsHarmless(t *testing.T) {
	h := hub.New()
	_, unsub := h.Subscribe("alpha")
	unsub()
	unsub()
}

func TestResubscribingAfterTheLastUnsubscribeWorks(t *testing.T) {
	h := hub.New()
	_, unsub := h.Subscribe("alpha")
	unsub() // the workspace now has no subscribers at all

	ch, unsub := h.Subscribe("alpha")
	defer unsub()
	h.Publish("alpha")

	if !woken(ch) {
		t.Error("a subscriber added after the workspace emptied was not woken")
	}
}

// Run with -race: this is the test that finds a hub whose map is touched
// outside its mutex.
func TestConcurrentSubscribersAreAllWoken(t *testing.T) {
	h := hub.New()
	const n = 50

	var ready, woke sync.WaitGroup
	ready.Add(n)
	woke.Add(n)
	release := make(chan struct{})
	for range n {
		go func() {
			ch, unsub := h.Subscribe("alpha")
			defer unsub()
			ready.Done()
			select {
			case <-ch:
				woke.Done()
			case <-release:
			}
		}()
	}
	ready.Wait()

	// Publish from several goroutines at once, while others churn
	// subscriptions on the same workspace.
	var busy sync.WaitGroup
	for range 10 {
		busy.Add(2)
		go func() {
			defer busy.Done()
			h.Publish("alpha")
		}()
		go func() {
			defer busy.Done()
			_, unsub := h.Subscribe("alpha")
			unsub()
		}()
	}
	if !within(2*time.Second, busy.Wait) {
		t.Fatal("concurrent Publish and Subscribe calls did not finish; is Publish blocking?")
	}

	if !within(2*time.Second, woke.Wait) {
		close(release)
		t.Fatal("not every subscriber was woken")
	}
}

// within reports whether wait returns before d elapses.
func within(d time.Duration, wait func()) bool {
	done := make(chan struct{})
	go func() {
		wait()
		close(done)
	}()
	select {
	case <-done:
		return true
	case <-time.After(d):
		return false
	}
}
