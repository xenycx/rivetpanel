package live

import "testing"

func TestHubRoutesAndBounds(t *testing.T) {
	h := NewHub()
	h.MaxPerUser = 2
	a, _ := h.Subscribe("alice")
	b, _ := h.Subscribe("bob")
	if _, ok := h.Subscribe("alice"); !ok {
		t.Fatal("second stream refused")
	}
	if _, ok := h.Subscribe("alice"); ok {
		t.Fatal("third stream allowed")
	}
	h.Publish(Event{Topic: "notifications", Users: []string{"bob"}})
	h.Publish(Event{Topic: "tickets", Origin: "tab1"})
	if e := <-a.C; e.Topic != "tickets" || e.Origin != "tab1" {
		t.Fatalf("alice got %+v", e)
	}
	if e := <-b.C; e.Topic != "notifications" {
		t.Fatalf("bob got %+v", e)
	}
	// A full channel drops the oldest instead of blocking.
	for i := 0; i < 100; i++ {
		h.Publish(Event{Topic: "bots"})
	}
	if len(a.C) != cap(a.C) {
		t.Fatalf("buffer %d", len(a.C))
	}
	a.Close()
	a.Close()
	if h.Open() != 2 {
		t.Fatalf("open = %d", h.Open())
	}
	var nilHub *Hub
	nilHub.Publish(Event{Topic: "x"})
}
