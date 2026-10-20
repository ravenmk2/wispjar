package runtime

import (
	"sync"
	"testing"
)

func TestBusConcurrentPublishAndUnsubscribe(t *testing.T) {
	bus := NewBus()
	var wg sync.WaitGroup
	for range 12 {
		wg.Go(func() {
			for range 100 {
				_, cancelSession := bus.SubscribeSession("session", 2)
				_, cancelControl := bus.SubscribeControl("org", 2)
				bus.Publish("session", FollowFrame{})
				bus.PublishControl("org", ControlFrame{})
				cancelSession()
				cancelControl()
				// Stream shutdown and socket shutdown can race.
				cancelSession()
				cancelControl()
			}
		})
	}
	wg.Wait()
}
