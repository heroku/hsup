package main

import (
	"archive/tar"
	"bytes"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"time"

	"github.com/fsouza/go-dockerclient"
)

type StackImage struct {
	stack string
	image docker.APIImages
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

	images, err := d.c.ListImages(docker.ListImagesOptions{All: true})
	if err != nil {
		return nil, err
	}

	for _, image := range images {
		for _, tag := range image.RepoTags {
			if tag == stack {
				si.image = image
				return &si, nil
			}
		}
	}

	return nil, nil
}

func (d *Docker) BuildSlugImage(si *StackImage, release *Release) (string, error) {
	t := time.Now()
	inputBuf, outputBuf := bytes.NewBuffer(nil), bytes.NewBuffer(nil)
	tr := tar.NewWriter(inputBuf)
	defer tr.Close()

	dockerContents := fmt.Sprintf(`FROM %s
RUN groupadd -r app && useradd -r -g app app
RUN mkdir /app
RUN chown app:app /app
COPY hsup /tmp/hsup
RUN chmod a+x /tmp/hsup
RUN setuidgid app env HEROKU_ACCESS_TOKEN=%s /tmp/hsup -d abspath build -a %s
RUN rm /tmp/hsup
WORKDIR /app
`, si.image.ID, os.Getenv("HEROKU_ACCESS_TOKEN"), release.appName)

	log.Println(dockerContents)
	tr.WriteHeader(&tar.Header{
		Name:    "Dockerfile",
		Size:    int64(len(dockerContents)),
		ModTime: t, AccessTime: t,
		ChangeTime: t})
	tr.Write([]byte(dockerContents))

	hsupBytes, err := ioutil.ReadFile(os.Args[0])
	if err != nil {
		return "", err
	}

	tr.WriteHeader(&tar.Header{
		Name:    "hsup",
		Size:    int64(len(hsupBytes)),
		ModTime: t, AccessTime: t,
		ChangeTime: t})
	tr.Write([]byte(hsupBytes))
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
