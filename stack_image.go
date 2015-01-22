package hsup

import (
	"bufio"
	"compress/gzip"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/htcat/htcat"
	"gopkg.in/yaml.v2"
)

const HEROKU_STACKS_MANIFEST_URL = "https://s3.amazonaws.com/heroku_stacks_production/manifest.yml"

type HerokuStackImage struct {
	Name    string
	Version string
	URL     string `yaml:"url"`
	Md5     string
	Primary bool

	basedir string
}

func HerokuStacksFromManifest(stacksDir string) ([]HerokuStackImage, error) {
	// TODO: cache the manifest
	resp, err := http.Get(HEROKU_STACKS_MANIFEST_URL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf(
			"invalid stacks manifest at %q",
			HEROKU_STACKS_MANIFEST_URL,
		)
	}
	manifest, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var stacks []HerokuStackImage
	if err := yaml.Unmarshal(manifest, &stacks); err != nil {
		return nil, err
	}
	for i, _ := range stacks {
		stacks[i].basedir = stacksDir
	}
	return stacks, nil
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
	if contents, err := ioutil.ReadDir(imgDir); err != nil {
		return err
	} else if len(contents) != 0 {
		return nil // already mounted
	}
	var flags uintptr = syscall.MS_NOATIME | syscall.MS_NODIRATIME |
		syscall.MS_NOSUID | syscall.MS_NODEV | syscall.MS_RDONLY
	return syscall.Mount(imgFile, imgDir, "", flags, "")
}

func (img *HerokuStackImage) fetch() error {
	// TODO check md5
	pr, pw := io.Pipe()
	defer pr.Close()
	defer pw.Close()

	u, err := url.Parse(img.URL)
	if err != nil {
		return err
	}

	dlResult := make(chan error)
	go func() {
		client := *http.DefaultClient
		if u.Scheme == "https" {
			client.Transport = &http.Transport{
				TLSClientConfig: &tls.Config{},
			}
		}
		// buffer downloads in case decompression can't keep up
		w := bufio.NewWriter(pw)
		_, err := htcat.New(&client, u, 5).WriteTo(w)
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
	return <-dlResult
}
