package main

import (
	"github.com/cyberdelia/heroku-go/v3"
	"log"
	"time"
)

type ApiPoller struct {
	Cl  *heroku.Service
	App string
	Dd  DynoDriver

	lastReleaseID string
}

type ApiFormation struct {
	h *heroku.Formation
}

func (f *ApiFormation) Args() []string {
	return []string{"bash", "-c", f.h.Command}
}
func (f *ApiFormation) Quantity() int {
	return f.h.Quantity
}

func (f *ApiFormation) Type() string {
	return f.h.Type
}

// Listens for new releases by periodically polling the Heroku
// API. When a new release is detected it is sent to the returned
// channel.
func (ap *ApiPoller) Poll() <-chan *Processes {
	out := make(chan *Processes)
	go ap.pollSynchronous(out)
	return out
}

func (ap *ApiPoller) fetchLatest() (*heroku.Release, error) {
	releases, err := ap.Cl.ReleaseList(
		ap.App, &heroku.ListRange{Descending: true, Field: "version",
			Max: 1})
	if err != nil {
		return nil, err
	}

	if len(releases) < 1 {
		return nil, ErrNoReleases
	}

	return releases[0], nil
}

func (ap *ApiPoller) fillProcesses(rel *heroku.Release) (*Processes, error) {
	config, err := ap.Cl.ConfigVarInfo(ap.App)
	if err != nil {
		return nil, err
	}

	slug, err := ap.Cl.SlugInfo(ap.App, rel.Slug.ID)
	if err != nil {
		return nil, err
	}

	hForms, err := ap.Cl.FormationList(ap.App, &heroku.ListRange{})
	if err != nil {
		return nil, err
	}

	procs := Processes{
		r: &Release{
			appName: ap.App,
			config:  config,
			slugURL: slug.Blob.URL,
			version: rel.Version,
		},
		forms: make([]Formation, len(hForms), len(hForms)),
		dd:    ap.Dd,
	}

	for i, hForm := range hForms {
		procs.forms[i] = &ApiFormation{h: hForm}
	}

	return &procs, nil
}

func (ap *ApiPoller) pollOnce() (*Processes, error) {
	release, err := ap.fetchLatest()
	if err != nil {
		return nil, err
	}

	if release != nil && ap.lastReleaseID != release.ID {
		ap.lastReleaseID = release.ID

		log.Printf("New release %s detected", ap.lastReleaseID)
		return ap.fillProcesses(release)
	}

	return nil, nil
}

func (ap *ApiPoller) pollSynchronous(out chan<- *Processes) {
	for {
		procs, err := ap.pollOnce()
		if err != nil {
			log.Println("Could not fetch new release information:",
				err)
			goto wait
		}

		if procs != nil {
			out <- procs
		}

	wait:
		time.Sleep(10 * time.Second)
	}
}