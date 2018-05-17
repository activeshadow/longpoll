package longpoll

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"sync"

	"github.com/pkg/errors"
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

	server    string
	transport *http.Transport
	since     int64
}

func NewWatcher(s string, t *http.Transport) *Watcher {
	return &watcher{server: s, transport: t}
}

func (this *watcher) get(ctx context.Context, path string) (int, []byte, error) {
	cli := &http.Client{Transport: this.transport}
	uri := this.server + path

	for {
		req, err := http.NewRequest("GET", uri, nil)
		if err != nil {
			return 0, []byte{}, errors.Wrap(err, "creating new HTTP request")
		}

		resp, err := cli.Do(req.WithContext(ctx))
		if err != nil {
			if ctx.Err() != nil {
				return 0, []byte{}, errors.Wrap(ctx.Err(), "context activity during HTTP request")
			}

			return 0, []byte{}, errors.Wrap(err, "making HTTP request")
		}

		body, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			return resp.StatusCode, []byte{}, errors.Wrap(err, "reading HTTP response body")
		}

		resp.Body.Close()
		return resp.StatusCode, body, nil
	}

	// should never get here
	return 0, []byte{}, errors.New("unable to make GET request")
}

func (this *watcher) Watch(ctx context.Context, path string, events chan Event) error {
	for {
		path := fmt.Sprintf("%s?timeout=%d&since=%d", path, 120, this.since)
		code, body, err := this.get(ctx, path)
		if err != nil {
			return errors.Wrap(err, "get call -- watch function")
		}

		var r = Message{}
		err = json.Unmarshal(body, &r)
		if err != nil {
			return errors.Wrap(err, "unmarshaling longpoll publish data")
		}

		switch code {
		case http.StatusOK:
			this.Lock()
			this.since = r.Timestamp
			this.Unlock()

			for _, e := range r.Events {
				events <- e
			}
		case http.StatusGatewayTimeout:
			this.Lock()
			this.since = r.Timestamp
			this.Unlock()
		case http.StatusBadRequest, http.StatusInternalServerError:
			return errors.Wrap(errors.New(r.Error), "from longpoll server")
		default:
			return fmt.Errorf("unknown response status code %d", code)
		}
	}

	// should never get here
	return errors.New("unable to watch for longpoll events")
}
