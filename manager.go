package longpoll

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/gofrs/uuid"
)

var ErrNoSubscribers = errors.New("no subscribers")

type event struct {
	Data      interface{} `json:"data"`
	timestamp int64
}

type message struct {
	Timestamp int64   `json:"timestamp"`
	Events    []event `json:"events"`
}

type client struct {
	id       uuid.UUID
	ctx      context.Context
	since    int64
	messages chan message
}

type Manager struct {
	sync.Mutex

	options managerOptions

	clients []client

	connections    chan client
	disconnections chan client
	events         chan event

	history    []event
	lvc        bool
	maxTimeout int
}

func NewManager(opts ...ManagerOption) *Manager {
	return &Manager{
		options:        newManagerOptions(opts...),
		connections:    make(chan client),
		disconnections: make(chan client),
		events:         make(chan event),
	}
}

func (this *Manager) Start(ctx context.Context) error {
	logger.Info("starting HTTP longpoll manager")

	for {
		select {
		case <-ctx.Done():
			return nil
		case cli := <-this.connections:
			this.add(cli)
		case cli := <-this.disconnections:
			this.remove(cli)
		case e := <-this.events:
			if this.options.lvc {
				this.history = []event{e}
			} else {
				this.history = append(this.history, e)
			}

			for _, c := range this.clients {
				if e.timestamp <= c.since { // would this ever happen?
					continue
				}

				if e.Data = this.options.callback(c.ctx, e.Data); e.Data == nil {
					continue
				}

				msg := message{Timestamp: e.timestamp, Events: []event{e}}
				c.messages <- msg
			}
		}
	}
}

func (this *Manager) add(cli client) {
	var (
		events []event
		tstamp = int64(-1)
	)

	// On initial connection, provide the new client with all the known historical
	// events that match its given category and since parameters.
	for _, e := range this.history {
		if e.timestamp <= cli.since {
			continue
		}

		events = append(events, e)

		if e.timestamp > tstamp {
			tstamp = e.timestamp
		}
	}

	// If we have no events to publish to the client right now, add it to the list
	// of clients to consider for the next published event.
	if events == nil {
		this.clients = append(this.clients, cli)
	} else {
		var toBeSent []event

		for _, e := range events {
			if e.Data = this.options.callback(cli.ctx, e.Data); e.Data != nil {
				toBeSent = append(toBeSent, e)
			}
		}

		if toBeSent != nil {
			cli.messages <- message{Timestamp: tstamp, Events: toBeSent}
		}
	}
}

func (this *Manager) remove(cli client) {
	idx := -1

	for i, c := range this.clients {
		if c.id == cli.id {
			idx = i
			break
		}
	}

	if idx >= 0 {
		this.clients = append(this.clients[:idx], this.clients[idx+1:]...)
	}
}

func (this *Manager) nowToMillisecondEpoch() int64 {
	this.Lock()
	defer this.Unlock()

	return MillisecondEpoch(time.Now())
}

func (this *Manager) Publish(data interface{}) error {
	e := event{
		Data:      data,
		timestamp: this.nowToMillisecondEpoch(),
	}

	logger.V(9).Info("publishing event", "event", e)

	this.events <- e

	if len(this.clients) == 0 {
		return ErrNoSubscribers
	}

	return nil
}

/*
Status Codes Returned:
	* 200 - timestamp and events as JSON
	* 400 - error as JSON
	* 500 - error as JSON
	* 504 - timestamp as JSON
*/

func (this *Manager) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	logger := logger.WithValues("url", r.URL)

	logger.V(5).Info("handling HTTP request")

	// We'll return JSON no matter what
	w.Header().Set("Content-Type", "application/json")

	// Don't cache response
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate") // HTTP 1.1
	w.Header().Set("Pragma", "no-cache")                                   // HTTP 1.0
	w.Header().Set("Expires", "0")                                         // Proxies

	t, err := strconv.Atoi(r.URL.Query().Get("timeout"))
	if err != nil || t > this.options.maxTimeout || t < 1 {
		logger.Error(nil, "invalid timeout request")

		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error": "invalid timeout parameter"}`))

		return
	}

	// Default to looking for all events (ie. `last` defaults to 0)
	var last int64

	// Since is string of milliseconds since epoch
	s := r.URL.Query().Get("since")

	if len(s) > 0 { // Client is requesting any event from given timestamp
		var err error

		last, err = strconv.ParseInt(s, 10, 64)
		if err != nil {
			logger.Error(err, "parsing since parameter", "since", s)

			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte(`{"error": "invalid since parameter"}`))

			return
		}
	}

	logger.V(9).Info("request parameters", "timeout", t, "since", last)

	var (
		id  = uuid.Must(uuid.NewV4())
		ctx = context.WithValue(r.Context(), "id", id)
	)

	if r.Method == http.MethodPost {
		if body, err := ioutil.ReadAll(r.Body); err == nil {
			ctx = context.WithValue(ctx, "body", body)
		}
	}

	cli := client{id: id, ctx: ctx, since: last, messages: make(chan message, 1)}
	this.connections <- cli

	closed := w.(http.CloseNotifier).CloseNotify()

	select {
	case msg := <-cli.messages:
		logger.V(7).Info("response for request", "msg", msg)

		m, err := json.Marshal(msg)
		if err != nil {
			logger.Error(err, "marshaling JSON")

			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(`{"error": "JSON marshal failure"}`))
		} else {
			w.Write(m)
		}
	case <-time.After(time.Duration(t) * time.Second):
		logger.V(7).Info("timeout reached")

		msg := fmt.Sprintf(`{"timestamp": %d}`, this.nowToMillisecondEpoch())

		w.WriteHeader(http.StatusGatewayTimeout)
		w.Write([]byte(msg))
	case <-closed:
		logger.V(7).Info("client closed connection")
	}

	this.disconnections <- cli
}
