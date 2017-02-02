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

var logger = logging.MustGetLogger("longpoll")

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

func NewManager() *manager {
	m := &manager{
		clients:        make([]client, 0),
		connections:    make(chan client),
		disconnections: make(chan client),
		events:         make(chan event),
		history:        make([]event, 0),
		maxTimeout:     120,
	}

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

	this.Unlock()
}

func (this *manager) remove(cli client) {
	this.Lock()

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

	this.Unlock()
}

func (this *manager) historize(e event) {
	this.Lock()
	this.history = append(this.history, e)
	this.Unlock()
}

func (this manager) Publish(category string, data interface{}) error {
	e := event{
		Timestamp: time.Now().UnixNano() / int64(time.Millisecond),
		Category:  category,
		Data:      data,
	}

	this.historize(e)
	logger.Debugf("PUBLISHING %#v\n", e)
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
		logger.Errorf("Invalid timeout parameter %q\n", r.URL.Query().Get("timeout"))
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error": "Invalid timeout parameter"}`))
		return
	}

	c := r.URL.Query().Get("category")
	if len(c) == 0 || len(c) > 1024 {
		logger.Error("Invalid category parameter -- must be 1-1024 characters long.")
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error": "Invalid category parameter."}`))
		return
	}

	// Default to looking for all events
	last := time.Now().UnixNano() / int64(time.Millisecond)

	// since is string of milliseconds since epoch
	s := r.URL.Query().Get("since")

	if len(s) > 0 { // Client is requesting any event from given timestamp
		var err error

		last, err = strconv.ParseInt(s, 10, 64)
		if err != nil {
			logger.Errorf("Error parsing since parameter %s\n", s)
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte(`{"error": "Invalid since parameter."}`))
			return
		}
	}

	cli := client{id: uuid.NewV4(), matcher: c, since: last, messages: make(chan message, 1)}
	this.connections <- cli

	closed := w.(http.CloseNotifier).CloseNotify()

	select {
	case msg := <-cli.messages:
		logger.Debugf("WRITING %#v\n", msg)

		m, err := json.Marshal(msg)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(`{"error": "JSON marshal failure"}`))
		}

		w.Write(m)
	case <-time.After(time.Duration(t) * time.Second):
		w.WriteHeader(http.StatusGatewayTimeout)
		w.Write(timeout{}.ToJSON())
	case <-closed:
		this.disconnections <- cli
		logger.Debugf("Closing HTTP request at %s\n", r.URL)
	}
}
