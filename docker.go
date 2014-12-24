package main

import (
	"archive/tar"
	"bytes"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/fsouza/go-dockerclient"
)

type StackImage struct {
	stack string
	img   docker.APIImages
}

type Docker struct {
	c *docker.Client
}

func (d *Docker) Connect() (err error) {
	endpoint := os.Getenv("DOCKER_HOST")
	if endpoint == "" {
		endpoint = "unix:///var/run/docker.sock"
	}
	log.Printf("DOCKER_HOST = %v\n", endpoint)

	certPath := os.Getenv("DOCKER_CERT_PATH")
	log.Printf("DOCKER_CERT_PATH = %v\n", certPath)
	if certPath == "" {
		d.c, err = docker.NewClient(endpoint)
	} else {
		cert := certPath + "/cert.pem"
		key := certPath + "/key.pem"
		ca := certPath + "/ca.pem"
		d.c, err = docker.NewTLSClient(endpoint, cert, key, ca)
	}
	return err
}

func (d *Docker) StackStat(stack string) (*StackImage, error) {
	si := StackImage{
		stack: stack,
	}

	imgs, err := d.c.ListImages(docker.ListImagesOptions{All: true})
	if err != nil {
		return nil, err
	}

	for _, img := range imgs {
		for _, tag := range img.RepoTags {
			if tag == stack {
				si.img = img
				return &si, nil
			}
		}
	}

	return nil, nil
}

func (d *Docker) BuildSlugImage(si *StackImage, release *Release) (string, error) {
	slugUrl := release.slugUrl
	t := time.Now()
	inputBuf, outputBuf := bytes.NewBuffer(nil), bytes.NewBuffer(nil)
	tr := tar.NewWriter(inputBuf)
	dockerContents := fmt.Sprintf(`FROM %s
RUN rm -rf /app
RUN curl '%s' -o /slug.img
RUN (unsquashfs -d /app /slug.img || (cd / && mkdir /app && tar -xzf /slug.img)) && rm -f /app/log /app/tmp && mkdir /app/log /app/tmp &&  chown -R daemon:daemon /app && chmod -R go+r /app && find /app -type d | xargs chmod go+x
WORKDIR /app
`, si.img.ID, slugUrl)

	log.Println(dockerContents)
	tr.WriteHeader(&tar.Header{
		Name:    "Dockerfile",
		Size:    int64(len(dockerContents)),
		ModTime: t, AccessTime: t,
		ChangeTime: t})
	tr.Write([]byte(dockerContents))
	tr.Close()

	imageName := release.Name()

	opts := docker.BuildImageOptions{
		Name:           imageName,
		InputStream:    inputBuf,
		OutputStream:   outputBuf,
		SuppressOutput: false,
	}

	if err := d.c.BuildImage(opts); err != nil {
		return "", err
	}

	return imageName, nil
}
