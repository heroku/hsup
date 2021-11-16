// +build linux

package hsup

import (
	"bufio"
	"compress/gzip"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/htcat/htcat"
	"gopkg.in/yaml.v2"
)

const (
	// currently available versions of Heroku stack images
	HerokuStacksManifestURL = "manifest.yml"
)

// HerokuStackImage models stack images as they are distributed by Heroku:
// binary disk images, usually intended to be mounted on loopback devices.
// The common use is to mount those images read-only, so a single immutable
// image can be shared by multiple containers, saving disk space and memory
// due to Linux CoW page sharing.
//
// Support for Heroku stack images is currently only enabled when building for
// Linux, because these images are currently only used by the libcontainer
// driver.
type HerokuStackImage struct {
	Name    string
	Version string
	URL     string `yaml:"url"`
	Md5     string
	Primary bool

	basedir string
}

func HerokuStacksFromManifest(stacksDir string) ([]HerokuStackImage, error) {
	manifest, err := fetchStacksManifestWithCache(stacksDir)
	if err != nil {
		return nil, err
	}
	var stacks []HerokuStackImage
	if err := yaml.Unmarshal(manifest, &stacks); err != nil {
		return nil, err
	}
	for i := range stacks {
		stacks[i].basedir = stacksDir
	}
	return stacks, nil
}

func fetchStacksManifestWithCache(stacksDir string) ([]byte, error) {
	cached := filepath.Join(stacksDir, "manifest.yml")
	if _, err := os.Stat(cached); err == nil {
		return ioutil.ReadFile(cached)
	}
	manifest, err := fetchStacksManifest()
	if err != nil {
		return nil, err
	}
	err = ioutil.WriteFile(cached, manifest, 0644)
	return manifest, err
}

func fetchStacksManifest() ([]byte, error) {
	resp, err := http.Get(HerokuStacksManifestURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf(
			"invalid stacks manifest at %q",
			HerokuStacksManifestURL,
		)
	}
	return ioutil.ReadAll(resp.Body)
}

func CurrentStackImagePath(stacksDir, name string) (string, error) {
	// TODO: check if it is really mounted
	// TODO: order by version numbers and pick the last
	names, err := filepath.Glob(filepath.Join(stacksDir, name+"-*"))
	if err != nil {
		return "", err
	}
	last := len(names) - 1
	for i := range names {
		n := names[last-i]
		if !strings.HasSuffix(n, ".img") {
			return n, nil
		}
	}

	return "", errors.New("no matching stack image found")
}

func (img *HerokuStackImage) Dir() string {
	return filepath.Join(img.basedir, img.Name+"-"+img.Version)
}

func (img *HerokuStackImage) Filename() string {
	return img.Dir() + ".img"
}

//TODO: avoid multiple processes trying to mount the same stack image
func (img *HerokuStackImage) mount() error {
	var (
		imgFile = img.Filename()
		imgDir  = img.Dir()
	)
	if _, err := os.Stat(imgFile); err != nil {
		if err := img.fetch(); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(imgDir, 0755); err != nil {
		return err
	}
	if contents, err := ioutil.ReadDir(imgDir); err != nil {
		return err
	} else if len(contents) != 0 {
		return nil // already mounted
	}
	log.Printf("Mounting stack image %q onto %q", imgFile, imgDir)
	out, err := exec.Command(
		"/bin/mount", "-o", "loop,nosuid,nodev,noatime,nodiratime,rw",
		imgFile, imgDir,
	).CombinedOutput()
	if err != nil {
		log.Println(string(out))
		return err
	}
	if err := img.addMissingDirectories(); err != nil {
		return err
	}
	return syscall.Mount("", imgDir, "", syscall.MS_REMOUNT|syscall.MS_RDONLY, "")

}

// addMissingDirectories is required until https://github.com/heroku/stack-images/pull/13
// gets merged.
func (img *HerokuStackImage) addMissingDirectories() error {
	return os.MkdirAll(filepath.Join(img.Dir(), "sys"), 0755)
}

//TODO: avoid multiple processes trying to fetch the same stack image
func (img *HerokuStackImage) fetch() error {
	log.Printf("Downloading stack image %q. This may take a while...", img.Name)
	// TODO check md5
	pr, pw := io.Pipe()
	defer pr.Close()

	u, err := url.Parse(img.URL)
	if err != nil {
		return err
	}

	// buffered channel: avoid blocking the writer goroutine
	dlResult := make(chan error, 1)
	go func() {
		// close the pipe as soon as the download finishes so readers receive EOF
		defer pw.Close()
		client := *http.DefaultClient
		if u.Scheme == "https" {
			client.Transport = &http.Transport{
				TLSClientConfig: &tls.Config{},
			}
		}
		// buffer the download in case gunzip can't keep up
		w := bufio.NewWriter(pw)
		_, err := htcat.New(&client, u, 5).WriteTo(w)
		w.Flush()
		dlResult <- err
	}()

	r, err := gzip.NewReader(pr)
	if err != nil {
		return err
	}
	defer r.Close()

	imageFile, err := os.Create(img.Filename())
	if err != nil {
		return err
	}
	defer imageFile.Close()
	if err := imageFile.Chmod(0644); err != nil {
		return err
	}

	if _, err := io.Copy(imageFile, r); err != nil {
		return err
	}
	log.Println("Stack image download finished")
	return <-dlResult
}
