package longpoll

import (
	"encoding/json"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/mattn/go-zglob"
	"github.com/op/go-logging"
	"github.com/satori/go.uuid"
)

var logger *logging.Logger

func millisecondStringToTime(ms string) (time.Time, error) {
	epoch, err := strconv.ParseInt(ms, 10, 64)
	if err != nil {
		return time.Time{}, err
	}

	return time.Unix(0, epoch*int64(time.Millisecond)).In(time.UTC), nil
}

type event struct {
	Timestamp int64       `json:"timestamp"`
	Category  string      `json:"category"`
	Data      interface{} `json:"data"`
}

type message struct {
	Timestamp int64   `json:"timestamp"`
	Events    []event `json:"events"`
}

type client struct {
	id       uuid.UUID
	matcher  string
	since    int64
	messages chan message
}

type timeout struct {
	Timeout   string `json:"timeout"`
	Timestamp int64  `json:"timestamp"`
}

func (this timeout) ToJSON() []byte {
	this.Timeout = "No events before timeout"
	this.Timestamp = time.Now().UnixNano() / int64(time.Millisecond)

	m, _ := json.Marshal(this)
	return m
}

type manager struct {
	sync.Mutex

	clients []client

	connections    chan client
	disconnections chan client
	events         chan event

	history    []event
	maxTimeout int
}

func NewManager(logModule string) *manager {
	logger = logging.MustGetLogger(logModule)

	m := &manager{
		clients:        make([]client, 0),
		connections:    make(chan client),
		disconnections: make(chan client),
		events:         make(chan event),
		history:        make([]event, 0),
		maxTimeout:     120,
	}

	logger.Notice("Starting HTTP longpoll publisher")

	go m.run()
	return m
}

func (this manager) run() {
	for {
		select {
		case cli := <-this.connections:
			this.add(cli)
		case cli := <-this.disconnections:
			this.remove(cli)
		case e := <-this.events:
			for _, c := range this.clients {
				if e.Timestamp <= c.since { // would this ever happen?
					continue
				}

				match, err := zglob.Match(c.matcher, e.Category)
				if err != nil {
					continue
				}

				if match {
					msg := message{Timestamp: e.Timestamp, Events: []event{e}}
					c.messages <- msg
				}
			}
		}
	}
}

func (this *manager) add(cli client) {
	this.Lock()
	defer this.Unlock()

	this.clients = append(this.clients, cli)

	var (
		events []event
		tstamp = int64(-1)
	)

	/*
		On initial connection, provide the new client with all the known
		historical events that match its given category and since parameters.
	*/
	for _, e := range this.history {
		if e.Timestamp <= cli.since { // would this ever happen?
			continue
		}

		match, err := zglob.Match(cli.matcher, e.Category)
		if err != nil {
			continue
		}

		if match {
			events = append(events, e)

			if e.Timestamp > tstamp {
				tstamp = e.Timestamp
			}
		}
	}

	if events != nil {
		cli.messages <- message{Timestamp: tstamp, Events: events}
	}
}

func (this *manager) remove(cli client) {
	this.Lock()
	defer this.Unlock()

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

func (this manager) Publish(category string, data interface{}) error {
	this.Lock()
	defer this.Unlock()

	e := event{
		Timestamp: time.Now().UnixNano() / int64(time.Millisecond),
		Category:  category,
		Data:      data,
	}

	logger.Debugf("Publishing event %#v\n", e)

	this.history = append(this.history, e)
	this.events <- e

	return nil
}

/*
Status Codes Returned:
	* 200 - timestamp, category, and data included
	* 400 - error included
	* 504 - timeout and timestamp included
*/

func (this manager) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	logger.Debugf("Handling HTTP request at %s\n", r.URL)

	// We are going to return json no matter what
	w.Header().Set("Content-Type", "application/json")

	// Don't cache response
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate") // HTTP 1.1
	w.Header().Set("Pragma", "no-cache")                                   // HTTP 1.0
	w.Header().Set("Expires", "0")                                         // Proxies

	t, err := strconv.Atoi(r.URL.Query().Get("timeout"))
	if err != nil || t > this.maxTimeout || t < 1 {
		logger.Errorf("Invalid timeout parameter for request %s\n", r.URL)
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error": "Invalid timeout parameter"}`))
		return
	}

	logger.Debugf("Timeout is set to %d seconds for request %s\n", t, r.URL)

	c := r.URL.Query().Get("category")
	if len(c) == 0 || len(c) > 1024 {
		logger.Errorf("Invalid category parameter for request %s\n", r.URL)
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error": "Invalid category parameter."}`))
		return
	}

	// Default to looking for all events (ie. `last` defaults to 0)
	var last int64

	// since is string of milliseconds since epoch
	s := r.URL.Query().Get("since")

	if len(s) > 0 { // Client is requesting any event from given timestamp
		var err error

		last, err = strconv.ParseInt(s, 10, 64)
		if err != nil {
			logger.Errorf("Error parsing since parameter %s for request %s\n", s, r.URL)
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte(`{"error": "Invalid since parameter."}`))
			return
		}
	}

	logger.Debugf("The `since` epoch timestamp is set to %d for request %s\n", last, r.URL)

	cli := client{id: uuid.NewV4(), matcher: c, since: last, messages: make(chan message, 1)}
	this.connections <- cli

	closed := w.(http.CloseNotifier).CloseNotify()

	select {
	case msg := <-cli.messages:
		logger.Debugf("Writing %#v for request %s\n", msg, r.URL)

		m, err := json.Marshal(msg)
		if err != nil {
			logger.Error("Error marshaling JSON: %s\n", err.Error())
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(`{"error": "JSON marshal failure"}`))
		}

		w.Write(m)
	case <-time.After(time.Duration(t) * time.Second):
		logger.Debugf("Timeout reached for request %s\n", r.URL)
		w.WriteHeader(http.StatusGatewayTimeout)
		w.Write(timeout{}.ToJSON())
	case <-closed:
		logger.Debugf("Client closed connection for request %s\n", r.URL)
	}

	this.disconnections <- cli
}
