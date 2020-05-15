package longpoll

import (
	"net/http"
	"time"
)

type ManagerOption func(*managerOptions)

type managerOptions struct {
	lvc        bool
	maxTimeout int
}

func newManagerOptions(opts ...ManagerOption) managerOptions {
	o := managerOptions{maxTimeout: 120}

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
