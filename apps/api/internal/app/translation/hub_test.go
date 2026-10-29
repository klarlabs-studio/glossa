package translationapp_test

import (
	"testing"
	"time"

	"github.com/google/uuid"

	translationapp "go.klarlabs.de/glossa/apps/api/internal/app/translation"
)

func TestHub_PublishFansOutToSubscribers(t *testing.T) {
	h := translationapp.NewHub()
	projectID := uuid.New()

	s1 := h.Subscribe(projectID, 0)
	s2 := h.Subscribe(projectID, 0)
	defer h.Unsubscribe(projectID, s1)
	defer h.Unsubscribe(projectID, s2)

	h.Publish(projectID, uuid.New(), translationapp.Event{Type: "translation.updated", Key: "x", Value: "v"})

	for _, sub := range []*translationapp.Subscriber{s1, s2} {
		select {
		case e := <-sub.C:
			if e.Key != "x" || e.Value != "v" || e.ID == 0 {
				t.Fatalf("bad event: %+v", e)
			}
		case <-time.After(time.Second):
			t.Fatal("subscriber did not receive event")
		}
	}
}

func TestHub_IsolatesProjects(t *testing.T) {
	h := translationapp.NewHub()
	a, b := uuid.New(), uuid.New()

	sa := h.Subscribe(a, 0)
	sb := h.Subscribe(b, 0)
	defer h.Unsubscribe(a, sa)
	defer h.Unsubscribe(b, sb)

	h.Publish(a, uuid.New(), translationapp.Event{Key: "for-a"})

	select {
	case e := <-sa.C:
		if e.Key != "for-a" {
			t.Fatalf("wrong event on sa: %+v", e)
		}
	case <-time.After(time.Second):
		t.Fatal("sa did not receive event")
	}

	select {
	case e := <-sb.C:
		t.Fatalf("project b leaked an event from project a: %+v", e)
	case <-time.After(50 * time.Millisecond):
		// expected silence
	}
}

func TestHub_AssignsMonotonicIDs(t *testing.T) {
	h := translationapp.NewHub()
	projectID := uuid.New()
	s := h.Subscribe(projectID, 0)
	defer h.Unsubscribe(projectID, s)

	for i := 0; i < 5; i++ {
		h.Publish(projectID, uuid.New(), translationapp.Event{Key: "k"})
	}

	var last uint64
	for i := 0; i < 5; i++ {
		select {
		case e := <-s.C:
			if e.ID <= last {
				t.Fatalf("non-monotonic id: prev=%d this=%d", last, e.ID)
			}
			last = e.ID
		case <-time.After(time.Second):
			t.Fatalf("event %d missing", i)
		}
	}
}

func TestHub_ReplaysHistoryAfterLastEventID(t *testing.T) {
	h := translationapp.NewHub()
	projectID := uuid.New()

	// Publish three events with no subscriber — they land in the
	// ring buffer.
	for i := 0; i < 3; i++ {
		h.Publish(projectID, uuid.New(), translationapp.Event{Key: "k"})
	}

	// Reconnect after the first event — should receive 2 + 3 only.
	s := h.Subscribe(projectID, 1)
	defer h.Unsubscribe(projectID, s)

	var got []uint64
	for i := 0; i < 2; i++ {
		select {
		case e := <-s.C:
			got = append(got, e.ID)
		case <-time.After(time.Second):
			t.Fatalf("replay event %d missing", i)
		}
	}
	if len(got) != 2 || got[0] != 2 || got[1] != 3 {
		t.Fatalf("expected ids [2,3], got %v", got)
	}
}

func TestHub_DropsSlowSubscriberWithoutBlockingPublisher(t *testing.T) {
	h := translationapp.NewHub()
	projectID := uuid.New()
	s := h.Subscribe(projectID, 0)

	// Overflow the 32-event buffer; once it fills, the next
	// publish should drop the subscriber rather than block.
	for i := 0; i < 64; i++ {
		h.Publish(projectID, uuid.New(), translationapp.Event{Key: "k"})
	}

	// Drain — channel must be closed.
	for range s.C {
	}
}
