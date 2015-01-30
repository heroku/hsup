package diag

import (
	"io"
	"log"
	"os"
)

var DefaultDiag = New(1024 * 1024)

func Log(values ...interface{}) {
	DefaultDiag.Log(values...)
}

func Logf(s string, values ...interface{}) {
	DefaultDiag.Logf(s, values...)
}

func Contents() []string {
	return DefaultDiag.Contents()
}

func init() {
	w := io.MultiWriter(DefaultDiag, os.Stderr)
	log.SetOutput(w)
}
