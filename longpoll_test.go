package longpoll

import (
	"context"
	"errors"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestLongpoll(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mgr := NewManager(LVC(false))
	go mgr.Start(ctx)

	mux := http.NewServeMux()
	mux.Handle("/events/watch", mgr)

	var (
		pub  = httptest.NewServer(mux)
		cli  = &http.Client{}
		done = make(chan error)
	)

	req, _ := http.NewRequest("GET", pub.URL+"/events/watch?timeout=120", nil)

	go func() {
		resp, err := cli.Do(req)
		if err != nil {
			done <- err
			return
		}

		defer resp.Body.Close()

		body, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			done <- err
			return
		}

		t.Log(string(body))
		close(done)
	}()

	mgr.Publish(map[string]string{"hello": "world!"})
	err := <-done

	if err != nil {
		t.Log(err)
		t.FailNow()
	}
}

func TestWatcher(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mgr := NewManager(LVC(false))
	go mgr.Start(ctx)

	mux := http.NewServeMux()
	mux.Handle("/events/watch", mgr)

	var (
		pub     = httptest.NewServer(mux)
		events  = make(chan Event)
		done    = make(chan struct{})
		watcher = NewWatcher(Endpoint(pub.URL), Transport(http.DefaultTransport.(*http.Transport)), Since(time.Time{}))
	)

	go func() {
		event := <-events
		t.Log(string(event.Data))
		close(done)
	}()

	go watcher.Watch(ctx, "/events/watch", events)

	mgr.Publish(map[string]string{"hello": "world!"})
	<-done
}

func TestUnauthorizedClient(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cb := func(context.Context, interface{}) interface{} { return nil }

	mgr := NewManager(LVC(false), EventCallback(cb))
	go mgr.Start(ctx)

	mux := http.NewServeMux()
	mux.Handle("/events/watch", mgr)

	var (
		pub  = httptest.NewServer(mux)
		cli  = &http.Client{}
		done = make(chan error)
	)

	req, _ := http.NewRequest("GET", pub.URL+"/events/watch?timeout=1", nil)

	go func() {
		resp, err := cli.Do(req)
		if err != nil {
			done <- err
			return
		}

		defer resp.Body.Close()

		if resp.StatusCode != http.StatusGatewayTimeout {
			done <- errors.New("expected 504 status")
			return
		}

		close(done)
	}()

	mgr.Publish(map[string]string{"hello": "world!"})
	err := <-done

	if err != nil {
		t.Log(err)
		t.FailNow()
	}
}
