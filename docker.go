package main

import (
	"archive/tar"
	"bytes"
	"fmt"
	"github.com/fsouza/go-dockerclient"
	"os"
	"time"
)

type StackImage struct {
	stack   string
	repoTag string
	img     docker.APIImages
}

type Docker struct {
	c *docker.Client
}

func (d *Docker) Connect() (err error) {
	endpoint := os.Getenv("DOCKER_HOST")
	if endpoint == "" {
		endpoint = "unix:///var/run/docker.sock"
	}
	d.c, err = docker.NewClient(endpoint)
	return err
}

func (d *Docker) StackStat(stack string) (*StackImage, error) {
	si := StackImage{}
	switch stack {
	case "cedar-14":
		si.repoTag = "heroku/cedar:14"
	default:
		return nil, fmt.Errorf("unrecognized stack: %s", stack)
	}

	si.stack = stack

	imgs, err := d.c.ListImages(docker.ListImagesOptions{All: true})
	if err != nil {
		return nil, err
	}

	for _, img := range imgs {
		for _, tag := range img.RepoTags {
			if tag == si.repoTag {
				si.img = img
			}
		}
	}

	return &si, nil
}

func (d *Docker) BuildSlugImage(si *StackImage) error {
	t := time.Now()
	inputBuf, outputBuf := bytes.NewBuffer(nil), bytes.NewBuffer(nil)
	tr := tar.NewWriter(inputBuf)
	dockerContents := fmt.Sprintf(`FROM %s
RUN rm -rf /app
RUN curl '%s' -o /slug.img
RUN (unsquashfs -d /app /slug.img || (cd / && mkdir /app && tar -xzf /slug.img)) && rm -f /app/log /app/tmp && mkdir /app/log /app/tmp &&  chown -R daemon:daemon /app && chmod -R gor /app && find /app -type d | xargs chmod gox
WORKDIR /app
`, si.img.ID, "http://localhost/bogus")
	tr.WriteHeader(&tar.Header{
		Name:    "Dockerfile",
		Size:    int64(len(dockerContents)),
		ModTime: t, AccessTime: t,
		ChangeTime: t})
	tr.Write([]byte(dockerContents))
	tr.Close()

	opts := docker.BuildImageOptions{
		Name:         "hsup",
		InputStream:  inputBuf,
		OutputStream: outputBuf,
	}

	if err := d.c.BuildImage(opts); err != nil {
		return err
	}

	return nil
}
