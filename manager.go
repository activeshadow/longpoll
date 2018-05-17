package longpoll

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/satori/go.uuid"
)

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
	since    int64
	messages chan message
}

type Manager struct {
	sync.Mutex

	clients []client

	connections    chan client
	disconnections chan client
	events         chan event

	history    []event
	lvc        bool
	maxTimeout int
}

func NewManager(lvc bool) *Manager {
	m := &Manager{
		clients:        make([]client, 0),
		connections:    make(chan client),
		disconnections: make(chan client),
		events:         make(chan event),
		history:        make([]event, 0),
		lvc:            lvc,
		maxTimeout:     120,
	}

	log.Println("Starting HTTP longpoll publisher")

	go m.run()
	return m
}

func (this *Manager) run() {
	for {
		select {
		case cli := <-this.connections:
			this.add(cli)
		case cli := <-this.disconnections:
			this.remove(cli)
		case e := <-this.events:
			if this.lvc {
				this.history = []event{e}
			} else {
				this.history = append(this.history, e)
			}

			for _, c := range this.clients {
				if e.timestamp <= c.since { // would this ever happen?
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
		cli.messages <- message{Timestamp: tstamp, Events: events}
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

	return time.Now().UnixNano() / int64(time.Millisecond)
}

func (this *Manager) Publish(data interface{}) error {
	e := event{
		Data:      data,
		timestamp: this.nowToMillisecondEpoch(),
	}

	log.Printf("Publishing event %#v\n", e)

	this.events <- e
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
	log.Printf("Handling HTTP request at %s\n", r.URL)

	// We'll return JSON no matter what
	w.Header().Set("Content-Type", "application/json")

	// Don't cache response
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate") // HTTP 1.1
	w.Header().Set("Pragma", "no-cache")                                   // HTTP 1.0
	w.Header().Set("Expires", "0")                                         // Proxies

	t, err := strconv.Atoi(r.URL.Query().Get("timeout"))
	if err != nil || t > this.maxTimeout || t < 1 {
		log.Printf("Invalid timeout parameter for request %s\n", r.URL)

		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error": "invalid timeout parameter"}`))

		return
	}

	log.Printf("Timeout is set to %d seconds for request %s\n", t, r.URL)

	// Default to looking for all events (ie. `last` defaults to 0)
	var last int64

	// Since is string of milliseconds since epoch
	s := r.URL.Query().Get("since")

	if len(s) > 0 { // Client is requesting any event from given timestamp
		var err error

		last, err = strconv.ParseInt(s, 10, 64)
		if err != nil {
			log.Printf("Error parsing since parameter %s for request %s\n", s, r.URL)

			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte(`{"error": "invalid since parameter"}`))

			return
		}
	}

	log.Printf("The `since` epoch timestamp is set to %d for request %s\n", last, r.URL)

	cli := client{id: uuid.NewV4(), since: last, messages: make(chan message, 1)}
	this.connections <- cli

	closed := w.(http.CloseNotifier).CloseNotify()

	select {
	case msg := <-cli.messages:
		log.Printf("Writing %#v for request %s\n", msg, r.URL)

		m, err := json.Marshal(msg)
		if err != nil {
			log.Println("Error marshaling JSON: %s\n", err.Error())

			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(`{"error": "JSON marshal failure"}`))
		} else {
			w.Write(m)
		}
	case <-time.After(time.Duration(t) * time.Second):
		log.Printf("Timeout reached for request %s\n", r.URL)

		msg := fmt.Sprintf(`{"timestamp": %d}`, this.nowToMillisecondEpoch())

		w.WriteHeader(http.StatusGatewayTimeout)
		w.Write([]byte(msg))
	case <-closed:
		log.Printf("Client closed connection for request %s\n", r.URL)
	}

	this.disconnections <- cli
}
