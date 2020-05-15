package longpoll

import (
	"actshad.dev/logr/nologr"
	"github.com/go-logr/logr"
)

var logger = nologr.New()

func SetLogger(l logr.Logger) {
	logger = l
}
