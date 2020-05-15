package longpoll

import (
	"context"
	"net/http"
	"time"
)

type eventCallback func(context.Context, interface{}) interface{}

type ManagerOption func(*managerOptions)

type managerOptions struct {
	lvc        bool
	maxTimeout int
	callback   eventCallback
}

func newManagerOptions(opts ...ManagerOption) managerOptions {
	o := managerOptions{
		maxTimeout: 120,
		callback:   func(_ context.Context, e interface{}) interface{} { return e },
	}

	for _, opt := range opts {
		opt(&o)
	}

	return o
}

func LVC(l bool) ManagerOption {
	return func(o *managerOptions) {
		o.lvc = l
	}
}

func MaxTimeout(m int) ManagerOption {
	return func(o *managerOptions) {
		o.maxTimeout = m
	}
}

func EventCallback(c eventCallback) ManagerOption {
	return func(o *managerOptions) {
		o.callback = c
	}
}

type WatcherOption func(*watcherOptions)

type watcherOptions struct {
	endpoint  string
	transport *http.Transport
	since     int64
}

func newWatcherOptions(opts ...WatcherOption) watcherOptions {
	var o watcherOptions

	for _, opt := range opts {
		opt(&o)
	}

	return o
}

func Endpoint(e string) WatcherOption {
	return func(o *watcherOptions) {
		o.endpoint = e
	}
}

func Transport(t *http.Transport) WatcherOption {
	return func(o *watcherOptions) {
		o.transport = t
	}
}

func Since(s time.Time) WatcherOption {
	return func(o *watcherOptions) {
		o.since = MillisecondEpoch(s)
	}
}
