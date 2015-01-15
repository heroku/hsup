package main

import (
	"fmt"
	"log"
	"encoding/json"
	"net/http"
)

var stateNames = map[DynoState]string{Stopped : "Starting", Started: "Started", Retiring: "Stopping", Retired: "Finished"}

type StatusResponse struct {
	Processes map[string]string
}

type StopRequest struct {
	Processes []string
}

type ControlAPI struct{
	processes *Processes
}

func (c *ControlAPI)Tee(procs  <-chan *Processes) <-chan *Processes {
	out := make(chan *Processes)
	go func(){
		for{
			p := <-procs
			c.processes = p
			out <- p
		}
	}()
	return out
}


func (c *ControlAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "GET":  c.ServeGET(w, r)
	case "POST": c.ServePOST(w, r)
	}
}

func (c *ControlAPI) ServeGET(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/status":
		resp := StatusResponse{make(map[string]string)}
		for _, e := range c.processes.executors {
			resp.Processes[e.processType] = stateNames[e.state]
		}
		enc := json.NewEncoder(w)
		w.WriteHeader(500)
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
		for _, p := range stop.Processes {
			for _, e := range c.processes.executors {
				if e.processType == p {
					log.Printf("Retiring %s", p)
					w.Write([]byte(p))
					e.Trigger(Retire)
				}
			}
		}
	}
}

func StartControlAPI(port int, processes <-chan *Processes) <-chan *Processes {
	api := &ControlAPI{}
	go http.ListenAndServe(fmt.Sprintf("127.0.0.1:%d", port), api)
	return api.Tee(processes)
}

