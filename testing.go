package radiko

import (
	"net/http"
	"os"
	"path/filepath"
)

const areaIDTokyo = "JP13"

var (
	outsideJP bool

	testdataDir string
)

func init() {
	// Skip tests if outside Japan.
	OUTSIDEJP := os.Getenv("GO_RADIKO_OUTSIDE_JP")
	if len(OUTSIDEJP) > 0 {
		outsideJP = true
	}

	GOPATH := os.Getenv("GOPATH")
	testdataDir = filepath.Join(GOPATH, "src", "github.com/yyoshiki41/go-radiko", "testdata")
}

// For skipping tests.
// radiko.jp restricts use from outside Japan.
func isOutsideJP() bool {
	return outsideJP
}

// Should restore defaultHTTPClient if SetHTTPClient is called.
func teardownHTTPClient() {
	SetHTTPClient(&http.Client{Timeout: defaultHTTPTimeout})
}
