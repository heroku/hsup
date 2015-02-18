package hsup

import (
	"encoding/json"
	"log"
	"net"
	"net/http"
)

type ProcessStatus struct {
	Status    string
	IPAddress string
	Port      int
}

type StatusResponse struct {
	Processes map[string]ProcessStatus
}

type StopRequest struct {
	Processes []string
}

type StopResponse struct {
	StoppedProcesses []string
}

type ControlAPI struct {
	*http.ServeMux
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

func (c *ControlAPI) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}

	resp := StatusResponse{make(map[string]ProcessStatus)}
	for _, e := range c.processes.Executors {
		resp.Processes[e.ProcessType] = ProcessStatus{
			IPAddress: e.IPAddress,
			Port:      e.Port,
			Status:    e.State.String(),
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (c *ControlAPI) handleControlStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}

	stop := new(StopRequest)
	if err := json.NewDecoder(r.Body).Decode(stop); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
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

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(StopResponse{stopped})
}

func StartControlAPI(socket string, processes <-chan *Processes) <-chan *Processes {
	api := newControlAPI()
	listener, err := net.Listen("unix", socket)
	if err != nil {
		panic(err)
	}
	go http.Serve(listener, api)
	return api.Tee(processes)
}

func newControlAPI() *ControlAPI {
	api := &ControlAPI{http.NewServeMux(), nil}
	api.HandleFunc("/control/stop", api.handleControlStop)
	api.HandleFunc("/status", api.handleStatus)

	return api
}
