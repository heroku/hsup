package hsup

import (
	"github.com/cyberdelia/heroku-go/v3"
	"log"
	"time"
)

type APIPoller struct {
	Cl *heroku.Service
	Hs *Startup

	lastReleaseID string
}

type APIFormation struct {
	h *heroku.Formation
}

func (f *APIFormation) Args() []string {
	return []string{"bash", "--login", "-c", f.h.Command}
}

func (f *APIFormation) Quantity() int {
	return f.h.Quantity
}

func (f *APIFormation) Type() string {
	return f.h.Type
}

// Listens for new releases by periodically polling the Heroku
// API. When a new release is detected it is sent to the returned
// channel.
func (ap *APIPoller) Notify() <-chan *Processes {
	out := make(chan *Processes)
	go ap.pollSynchronous(out)
	return out
}

func (ap *APIPoller) fetchLatest() (*heroku.Release, error) {
	releases, err := ap.Cl.ReleaseList(
		ap.Hs.App.Name,
		&heroku.ListRange{Descending: true, Field: "version", Max: 1})
	if err != nil {
		return nil, err
	}

	if len(releases) < 1 {
		return nil, ErrNoReleases
	}

	return releases[0], nil
}

func (ap *APIPoller) fillProcesses(rel *heroku.Release) (*Processes, error) {
	config, err := ap.Cl.ConfigVarInfo(ap.Hs.App.Name)
	if err != nil {
		return nil, err
	}

	slug, err := ap.Cl.SlugInfo(ap.Hs.App.Name, rel.Slug.ID)
	if err != nil {
		return nil, err
	}

	hForms, err := ap.Cl.FormationList(ap.Hs.App.Name, &heroku.ListRange{})
	if err != nil {
		return nil, err
	}

	procs := Processes{
		Rel: &Release{
			appName: ap.Hs.App.Name,
			config:  config,
			slugURL: slug.Blob.URL,
			version: rel.Version,
		},
		Forms:   make([]Formation, len(hForms), len(hForms)),
		Dd:      ap.Hs.Driver,
		OneShot: ap.Hs.OneShot,
	}

	for i, hForm := range hForms {
		procs.Forms[i] = &APIFormation{h: hForm}
	}

	return &procs, nil
}

func (ap *APIPoller) pollOnce() (*Processes, error) {
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

func (ap *APIPoller) pollSynchronous(out chan<- *Processes) {
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
