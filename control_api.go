package hsup

import (
	"encoding/json"
	"errors"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

type ProcessStatus struct {
	Status    string
	ProcessID int
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
	socket    string
	listener  net.Listener
}

var ErrSocketInUse = errors.New("socket in use")

const SocketPerm os.FileMode = 0770

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

func (c *ControlAPI) Listen() error {
	var err error
	c.listener, err = net.Listen("unix", c.socket)
	if err != nil {
		if !strings.Contains(err.Error(), "address already in use") {
			return err
		}

		if err := c.ping(); err == nil {
			return ErrSocketInUse
		} else {
			if _, err := os.Stat(c.socket); err == nil {
				if err := os.Remove(c.socket); err != nil {
					return err
				}
			} else {
				return err
			}
		}

		c.listener, err = net.Listen("unix", c.socket)
		if err != nil {
			return err
		}
	}

	if err := os.Chmod(c.socket, os.ModeSocket|SocketPerm); err != nil {
		return err
	}

	return http.Serve(c.listener, c)
}

func (c *ControlAPI) ping() error {
	client := &http.Client{
		Transport: &http.Transport{
			Dial: func(nwk, addr string) (net.Conn, error) {
				return net.Dial("unix", c.socket)
			},
		},
		Timeout: 5 * time.Second,
	}

	r, err := http.NewRequest("GET", "http://hsup/health", nil)
	if err != nil {
		return err
	}

	res, err := client.Do(r)
	if res != nil {
		res.Body.Close()
	}
	return err
}

func (c *ControlAPI) Close() {
	if c.listener != nil {
		c.listener.Close()
	}
}

func (c *ControlAPI) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Write([]byte("OK"))
}

func (c *ControlAPI) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}

	resp := StatusResponse{make(map[string]ProcessStatus)}
	for _, e := range c.processes.Executors {
		address, port := e.IPInfo()
		resp.Processes[e.ProcessType] = ProcessStatus{
			IPAddress: address,
			Port:      port,
			ProcessID: e.ProcessID,
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

func NewControlAPI(socket string, processes <-chan *Processes) (*ControlAPI, <-chan *Processes) {
	api := &ControlAPI{http.NewServeMux(), nil, socket, nil}
	api.HandleFunc("/control/stop", api.handleControlStop)
	api.HandleFunc("/status", api.handleStatus)
	api.HandleFunc("/health", api.handleHealth)

	return api, api.Tee(processes)
}
