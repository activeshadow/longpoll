package longpoll

import (
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLongpoll(t *testing.T) {
	mux := http.NewServeMux()
	mgr := NewManager("foobar", false)

	mux.Handle("/events/watch", mgr)

	pub := httptest.NewServer(mux)
	cli := &http.Client{}

	req, _ := http.NewRequest("GET", pub.URL+"/events/watch?timeout=120", nil)

	go func() {
		resp, err := cli.Do(req)
		if err != nil {
			t.FailNow()
		}

		defer resp.Body.Close()

		body, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			t.FailNow()
		}

		t.Log(string(body))
	}()

	mgr.Publish(map[string]string{"hello": "world!"})
}
