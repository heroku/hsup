package hsup

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
)

type StatusResponse struct {
	Processes map[string]string
}

type StopRequest struct {
	Processes []string
}

type StopResponse struct {
	StoppedProcesses []string
}

type ControlAPI struct {
	processes *Processes
}

func (c *ControlAPI) Tee(procs <-chan *Processes) <-chan *Processes {
	out := make(chan *Processes)
	go func() {
		for {
			p := <-procs
			c.processes = p
			out <- p
		}
	}()
	return out
}

func (c *ControlAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "GET":
		c.ServeGET(w, r)
	case "POST":
		c.ServePOST(w, r)
	}
}

func (c *ControlAPI) ServeGET(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/status":
		resp := StatusResponse{make(map[string]string)}
		for _, e := range c.processes.Executors {
			resp.Processes[e.ProcessType] = e.State.String()
		}
		enc := json.NewEncoder(w)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		enc.Encode(resp)
	}
}

func (c *ControlAPI) ServePOST(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/control/stop":
		stop := new(StopRequest)
		err := json.NewDecoder(r.Body).Decode(stop)
		if err != nil {
			w.WriteHeader(500)
			w.Write([]byte(err.Error()))
			return
		}
		stopped := []string{}
		for _, p := range stop.Processes {
			for _, e := range c.processes.Executors {
				if e.ProcessType == p {
					log.Printf("Retiring %s", p)
					e.Trigger(Retire)
					stopped = append(stopped, p)
				}
			}
		}
		enc := json.NewEncoder(w)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		enc.Encode(StopResponse{stopped})
	}
}

func StartControlAPI(port int, processes <-chan *Processes) <-chan *Processes {
	api := &ControlAPI{}
	go http.ListenAndServe(fmt.Sprintf("127.0.0.1:%d", port), api)
	return api.Tee(processes)
}
