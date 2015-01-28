package hsup

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"time"

	"github.com/fsouza/go-dockerclient"
)

type DockerStackImage struct {
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

func (d *Docker) StackStat(stack string) (*DockerStackImage, error) {
	si := DockerStackImage{
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

func (d *Docker) BuildSlugImage(si *DockerStackImage, release *Release) (
	string, error) {
	// Exit early if the image is already around.
	imageName := release.Name()
	if _, err := d.c.InspectImage(imageName); err == nil {
		// Successfully reuse the image that has -- probably
		// -- been built before for the release in question.
		// This avoids another long slug download, for
		// instance.
		return imageName, nil
	}

	t := time.Now()
	inputBuf, outputBuf := bytes.NewBuffer(nil), bytes.NewBuffer(nil)
	tr := tar.NewWriter(inputBuf)
	defer tr.Close()

	hs := Startup{Action: Build, Driver: &AbsPathDynoDriver{}}

	hsupBytes, err := ioutil.ReadFile(linuxAmd64Path())
	if err != nil {
		return "", err
	}

	tr.WriteHeader(&tar.Header{
		Name:    "hsup",
		Size:    int64(len(hsupBytes)),
		ModTime: t, AccessTime: t,
		ChangeTime: t})
	tr.Write([]byte(hsupBytes))

	isLocalTgz := false
	switch release.Where() {
	case Local:
		// A local file with a gzipped tarball.  Make it
		// available in the build.
		isLocalTgz = true
		slug, err := ioutil.ReadFile(release.slugURL)
		if err != nil {
			log.Fatalln("could not read slug",
				release.slugURL+":", err)
		}

		tr.WriteHeader(&tar.Header{
			Name:    "slug.tgz",
			Size:    int64(len(slug)),
			ModTime: t, AccessTime: t,
			ChangeTime: t})
		tr.Write(slug)
		hs.App.Slug = "/tmp/slug.tgz"
	case HTTP:
		// Rely on abspath driver for the fetch.
		hs.App.Slug = release.slugURL
	default:
		panic("unenumerated slug location")
	}

	// Generate Dockerfile and place in archive.
	genv := "HSUP_CONTROL_GOB=" + hs.ToBase64Gob()
	args := []string{"setuidgid", "app", "env", genv, "/tmp/hsup"}
	argText, err := json.Marshal(args)
	if err != nil {
		panic(fmt.Sprintln("could not marshal argv:", args))
	}

	var localSlugText string
	if isLocalTgz {
		localSlugText = "COPY slug.tgz /tmp/slug.tgz\n" +
			"RUN chmod a+r /tmp/slug.tgz"

	}

	dockerContents := fmt.Sprintf(`FROM %s
COPY hsup /tmp/hsup
RUN groupadd -r app && useradd -r -g app app && mkdir /app && chown app:app /app && chmod a+x /tmp/hsup
%s
RUN %s
RUN rm /tmp/hsup
WORKDIR /app
`, si.image.ID, localSlugText, argText)

	log.Println(dockerContents)
	tr.WriteHeader(&tar.Header{
		Name:    "Dockerfile",
		Size:    int64(len(dockerContents)),
		ModTime: t, AccessTime: t,
		ChangeTime: t})
	tr.Write([]byte(dockerContents))

	if os.IsNotExist(err) {
		log.Println("make a Linux binary: `make`")
	}

	tr.Close()

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
