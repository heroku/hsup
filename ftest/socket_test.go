package ftest

import (
	"bytes"
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/heroku/hsup"
)

func TestSocketGetStatus(t *testing.T) {
	socket := runProcess(t)
	client := newSocketClient(socket)

	if err := verifyStatus(t, client); err != nil {
		t.Fatal(err)
	}

	req, err := http.NewRequest("GET", "http://hsup/status", nil)
	if err != nil {
		t.Fatal(err)
	}

	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}

	var status hsup.StatusResponse
	if err := json.NewDecoder(res.Body).Decode(&status); err != nil {
		t.Fatal(err)
	}

	if err != nil {
		t.Fatal(err)
	}

	if was := len(status.Processes); was == 0 {
		t.Fatal("did not expect Processes to be 0")
	}

	if was := status.Processes["run"].Status; was == "" {
		t.Fatal("did not expect Status to be blank")
	}

	if was := status.Processes["run"].IPAddress; was == "" {
		t.Fatalf("did not expect IPAddress to be %q", was)
	}

	if strings.Contains(status.Processes["run"].IPAddress, "/") {
		t.Fatalf("IPAddress %q contains subnet", status.Processes["run"].IPAddress)
	}

	if was := status.Processes["run"].Port; was == 0 {
		t.Fatalf("did not expect Port to be %d", was)
	}
}

func TestSocketPostControlStop(t *testing.T) {
	socket := runProcess(t)
	client := newSocketClient(socket)

	if err := verifyStatus(t, client); err != nil {
		t.Fatal(err)
	}

	body := bytes.NewBuffer([]byte{})
	json.NewEncoder(body).Encode(hsup.StopRequest{
		Processes: []string{"run"},
	})

	req, err := http.NewRequest("POST", "http://hsup/control/stop", body)
	if err != nil {
		t.Fatal(err)
	}

	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}

	if was := res.StatusCode; was != http.StatusOK {
		t.Fatalf("expected %d; was %d", http.StatusOK, was)
	}

	var stop hsup.StopResponse
	if err := json.NewDecoder(res.Body).Decode(&stop); err != nil {
		t.Fatal(err)
	}

	if len(stop.StoppedProcesses) == 0 {
		t.Fatal("did not expect StoppedProcesses to be 0")
	}

	if was := stop.StoppedProcesses[0]; was != "run" {
		t.Fatal("expected %q; was %q", "run", was)
	}
}

func verifyStatus(t *testing.T, client *http.Client) error {
	req, err := http.NewRequest("GET", "http://hsup/status", nil)
	if err != nil {
		return err
	}

	success, err := retryUntil(30, 1*time.Second, func() (bool, error) {
		res, err := client.Do(req)
		if err != nil {
			return false, err
		}

		if was := res.StatusCode; was != http.StatusOK {
			return false, nil
		}

		var status hsup.StatusResponse
		if err := json.NewDecoder(res.Body).Decode(&status); err != nil {
			return false, err
		}

		if was := len(status.Processes); was == 0 {
			return false, nil
		}

		return true, nil
	})

	if err != nil {
		return err
	}

	if !success {
		t.Fatal("could not verify status endpoint")
	}

	return nil
}

func newSocketClient(socket string) *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			Dial: func(network, address string) (net.Conn, error) {
				return net.Dial("unix", socket)
			},
		},
	}
}

func runProcess(t *testing.T) string {
	socket := newSocketFile()
	go func(t *testing.T) {
		output, err := run(AppWithEnv, socket, []string{}, "sleep 5")
		debug(t, output)
		if err != nil {
			t.Fatal(err)
		}
	}(t)
	return socket
}
