package longpoll

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"sync"
)

type Event struct {
	Data json.RawMessage `json:"data"`
}

type Message struct {
	Error     string  `json:"error"`
	Timestamp int64   `json:"timestamp"`
	Events    []Event `json:"events"`
}

type Watcher struct {
	sync.RWMutex

	options watcherOptions
}

func NewWatcher(opts ...WatcherOption) *Watcher {
	return &Watcher{options: newWatcherOptions(opts...)}
}

func (this *Watcher) get(ctx context.Context, path string) (int, []byte, error) {
	cli := &http.Client{Transport: this.options.transport}
	uri := this.options.endpoint + path

	for {
		req, err := http.NewRequest("GET", uri, nil)
		if err != nil {
			return 0, []byte{}, fmt.Errorf("creating new HTTP request: %w", err)
		}

		resp, err := cli.Do(req.WithContext(ctx))
		if err != nil {
			if ctx.Err() != nil {
				return 0, []byte{}, fmt.Errorf("context activity during HTTP request: %w", ctx.Err())
			}

			return 0, []byte{}, fmt.Errorf("making HTTP request: %w", err)
		}

		body, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			return resp.StatusCode, []byte{}, fmt.Errorf("reading HTTP response body: %w", err)
		}

		resp.Body.Close()
		return resp.StatusCode, body, nil
	}
}

func (this *Watcher) Watch(ctx context.Context, path string, events chan Event) error {
	for {
		path := fmt.Sprintf("%s?timeout=%d&since=%d", path, 120, this.options.since)
		code, body, err := this.get(ctx, path)
		if err != nil {
			return fmt.Errorf("get call -- watch function: %w", err)
		}

		var r = Message{}
		err = json.Unmarshal(body, &r)
		if err != nil {
			return fmt.Errorf("unmarshaling longpoll publish data: %w", err)
		}

		switch code {
		case http.StatusOK:
			this.Lock()
			this.options.since = r.Timestamp
			this.Unlock()

			for _, e := range r.Events {
				events <- e
			}
		case http.StatusGatewayTimeout:
			this.Lock()
			this.options.since = r.Timestamp
			this.Unlock()
		case http.StatusBadRequest, http.StatusInternalServerError:
			return fmt.Errorf("from longpoll server: %s", r.Error)
		default:
			return fmt.Errorf("unknown response status code %d", code)
		}
	}
}
