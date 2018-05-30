package longpoll

import (
	"context"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestLongpoll(t *testing.T) {
	mux := http.NewServeMux()
	mgr := NewManager(false)

	mux.Handle("/events/watch", mgr)

	pub := httptest.NewServer(mux)
	cli := &http.Client{}
	done := make(chan struct{})

	req, _ := http.NewRequest("GET", pub.URL+"/events/watch?timeout=120", nil)

	go func() {
		resp, err := cli.Do(req)
		if err != nil {
			t.FailNow()
		}

		defer resp.Body.Close()

		body, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			t.FailNow()
		}

		t.Log(string(body))
		close(done)
	}()

	mgr.Publish(map[string]string{"hello": "world!"})
	<-done
}

func TestWatcher(t *testing.T) {
	mux := http.NewServeMux()
	mgr := NewManager(false)

	mux.Handle("/events/watch", mgr)

	pub := httptest.NewServer(mux)

	watcher := NewWatcher(pub.URL, http.DefaultTransport.(*http.Transport), time.Time{})
	events := make(chan Event)
	done := make(chan struct{})

	ctx, cancel := context.WithCancel(context.Background())

	go func() {
		event := <-events
		t.Log(string(event.Data))
		cancel()
		close(done)
	}()

	go watcher.Watch(ctx, "/events/watch", events)

	mgr.Publish(map[string]string{"hello": "world!"})
	<-done
}
