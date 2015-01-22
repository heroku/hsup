package hsup

import (
	"bytes"
	"encoding/base64"
	"encoding/gob"
	"strings"
)

type AppSerializable struct {
	Version   int
	Env       map[string]string
	Slug      string
	Stack     string
	Processes []FormationSerializable
}

type FormationSerializable struct {
	FArgs     []string `json:"Args"`
	FQuantity int      `json:"Quantity"`
	FType     string   `json:"Type"`
}

func (fs *FormationSerializable) Args() []string {
	return fs.FArgs
}
func (fs *FormationSerializable) Quantity() int {
	return fs.FQuantity
}

func (fs *FormationSerializable) Type() string {
	return fs.FType
}

func (as *AppSerializable) ToBase64Gob() string {
	buf := bytes.Buffer{}
	b64enc := base64.NewEncoder(base64.StdEncoding, &buf)
	enc := gob.NewEncoder(b64enc)
	err := enc.Encode(as)
	b64enc.Close()
	if err != nil {
		panic("could not encode gob:" + err.Error())
	}

	return buf.String()
}

func (as *AppSerializable) FromBase64Gob(payload string) {
	d := gob.NewDecoder(base64.NewDecoder(base64.StdEncoding,
		strings.NewReader(payload)))
	if err := d.Decode(as); err != nil {
		panic("could not decode gob:" + err.Error())
	}
}

func (as *AppSerializable) Procs(appName string, dd DynoDriver, oneShot bool) *Processes {
	procs := &Processes{
		Rel: &Release{
			appName: appName,
			config:  as.Env,
			slugURL: as.Slug,
			stack:   as.Stack,
			version: as.Version,
		},
		Forms:   make([]Formation, len(as.Processes)),
		Dd:      dd,
		OneShot: oneShot,
	}

	for i := range as.Processes {
		procs.Forms[i] = &as.Processes[i]
	}

	return procs
}
