package radiko

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yyoshiki41/go-radiko/internal/util"
)

func TestTimeshiftPlaylistM3U8(t *testing.T) {
	client := &Client{}

	_, err := client.TimeshiftPlaylistM3U8(context.Background(), "LFR", time.Now())
	if err != ErrTimeshiftPlaylistM3U8Removed {
		t.Errorf("expected ErrTimeshiftPlaylistM3U8Removed, but %v", err)
	}
}

const (
	fakeStationID = "LFR"
	fakeAuthToken = "fake-auth-token"
	segmentLength = 5 * time.Second
)

// fakeTimefreeServer imitates radiko.jp and the timefree playlist endpoint.
type fakeTimefreeServer struct {
	*httptest.Server

	// offset shifts EXT-X-PROGRAM-DATE-TIME from the 5-second grid.
	// Real timestamps are off by tens of milliseconds in both directions.
	offset time.Duration
	// drop is the start time (on the grid) of a segment that is never served.
	drop time.Time

	mu       sync.Mutex
	requests []url.Values
}

func newFakeTimefreeServer(t *testing.T) *fakeTimefreeServer {
	t.Helper()

	f := &fakeTimefreeServer{}
	mux := http.NewServeMux()
	mux.HandleFunc("/v3/program/date/20161112/JP13.xml", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `<?xml version="1.0" encoding="UTF-8"?>
<radiko>
  <stations>
    <station id="%s">
      <name>ニッポン放送</name>
      <progs>
        <date>20161112</date>
        <prog ft="20161112230000" to="20161112233000" ftl="2300" tol="2330" dur="1800"><title>test</title></prog>
        <prog ft="20161112233000" to="20161112233600" ftl="2330" tol="2336" dur="360"><title>short</title></prog>
      </progs>
    </station>
  </stations>
</radiko>`, fakeStationID)
	})
	mux.HandleFunc("/v3/station/stream/pc_html5/"+fakeStationID+".xml", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `<?xml version="1.0" encoding="UTF-8" ?>
<urls>
  <url areafree="1" max_delay="60" timefree="0"><playlist_create_url>%[1]s/so/playlist.m3u8</playlist_create_url></url>
  <url areafree="1" max_delay="60" timefree="1"><playlist_create_url>%[1]s/tf-areafree/playlist.m3u8</playlist_create_url></url>
  <url areafree="0" max_delay="60" timefree="0"><playlist_create_url>%[1]s/so/playlist.m3u8</playlist_create_url></url>
  <url areafree="0" max_delay="60" timefree="1"><playlist_create_url>%[1]s/tf/playlist.m3u8</playlist_create_url></url>
</urls>`, f.URL)
	})
	playlist := func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(radikoAuthTokenHeader) != fakeAuthToken {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		q := r.URL.Query()
		if l, err := strconv.Atoi(q.Get("l")); err != nil || l <= 0 || l > 300 {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		f.mu.Lock()
		recorded := url.Values{"path": {r.URL.Path}}
		for k, v := range q {
			recorded[k] = v
		}
		f.requests = append(f.requests, recorded)
		f.mu.Unlock()

		// A relative uri, like the real endpoint may return.
		fmt.Fprintf(w, "#EXTM3U\n#EXT-X-VERSION:6\n#EXT-X-STREAM-INF:PROGRAM-ID=1,BANDWIDTH=52973,CODECS=\"mp4a.40.5\"\nmedialist?start_at=%s&l=%s\n",
			q.Get("start_at"), q.Get("l"))
	}
	mux.HandleFunc("/tf/playlist.m3u8", playlist)
	mux.HandleFunc("/tf-areafree/playlist.m3u8", playlist)
	medialist := func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		start, err := util.ParseDatetime(q.Get("start_at"))
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		l, _ := strconv.Atoi(q.Get("l"))
		end := start.Add(time.Duration(l) * time.Second)

		fmt.Fprint(w, "#EXTM3U\n#EXT-X-VERSION:6\n#EXT-X-TARGETDURATION:5\n#EXT-X-MEDIA-SEQUENCE:1\n")
		// Like the real endpoint, serve only segments that start at or after start_at.
		for s := start.Truncate(segmentLength).Add(-segmentLength); s.Add(f.offset).Before(end); s = s.Add(segmentLength) {
			pdt := s.Add(f.offset)
			if pdt.Before(start) || s.Equal(f.drop) {
				continue
			}
			fmt.Fprintf(w, "#EXT-X-PROGRAM-DATE-TIME:%s\n#EXTINF:5.000,\n%s/segments/%s.aac\n",
				pdt.Format(time.RFC3339Nano), f.URL, util.Datetime(s))
		}
		fmt.Fprint(w, "#EXT-X-ENDLIST\n")
	}
	mux.HandleFunc("/tf/medialist", medialist)
	mux.HandleFunc("/tf-areafree/medialist", medialist)

	f.Server = httptest.NewServer(mux)
	t.Cleanup(f.Close)
	return f
}

func (f *fakeTimefreeServer) client(t *testing.T) *Client {
	t.Helper()

	u, err := url.Parse(f.URL)
	if err != nil {
		t.Fatal(err)
	}
	return &Client{
		URL:             u,
		httpClient:      f.Client(),
		authTokenHeader: fakeAuthToken,
		areaID:          areaIDTokyo,
	}
}

func fakeProgramStart(t *testing.T) time.Time {
	t.Helper()

	start, err := util.ParseDatetime("20161112230000")
	if err != nil {
		t.Fatal(err)
	}
	return start
}

func TestGetTimeshiftChunklist(t *testing.T) {
	cases := []struct {
		name   string
		ft, to string
		offset time.Duration
	}{
		{name: "30 minutes", ft: "20161112230000", to: "20161112233000", offset: 29 * time.Millisecond},
		// A segment that starts just before start_at is not served.
		{name: "30 minutes, early timestamps", ft: "20161112230000", to: "20161112233000", offset: -6 * time.Millisecond},
		// The last window is shorter than 300 seconds.
		{name: "6 minutes", ft: "20161112233000", to: "20161112233600", offset: -6 * time.Millisecond},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeTimefreeServer(t)
			f.offset = tc.offset
			ft, err := util.ParseDatetime(tc.ft)
			if err != nil {
				t.Fatal(err)
			}
			to, err := util.ParseDatetime(tc.to)
			if err != nil {
				t.Fatal(err)
			}

			chunklist, err := f.client(t).GetTimeshiftChunklist(context.Background(), fakeStationID, ft, false)
			if err != nil {
				t.Fatal(err)
			}

			if expected := int(to.Sub(ft) / segmentLength); len(chunklist) != expected {
				t.Fatalf("expected %d segments, but %d", expected, len(chunklist))
			}
			for i, c := range chunklist {
				expected := fmt.Sprintf("%s/segments/%s.aac", f.URL, util.Datetime(ft.Add(time.Duration(i)*segmentLength)))
				if c != expected {
					t.Fatalf("chunklist[%d]: expected %s, but %s", i, expected, c)
				}
			}

			lsid := f.requests[0].Get("lsid")
			if len(lsid) != 32 {
				t.Errorf("invalid lsid: %s", lsid)
			}
			for i, q := range f.requests {
				if q.Get("type") != "b" || q.Get("lsid") != lsid || q.Get("ft") != tc.ft || q.Get("to") != tc.to {
					t.Errorf("requests[%d]: unexpected query %v", i, q)
				}
			}
		})
	}
}

func TestGetTimeshiftChunklist_Areafree(t *testing.T) {
	f := newFakeTimefreeServer(t)

	_, err := f.client(t).GetTimeshiftChunklist(context.Background(), fakeStationID, fakeProgramStart(t), true)
	if err != nil {
		t.Fatal(err)
	}
	for i, q := range f.requests {
		if q.Get("type") != "c" || q.Get("path") != "/tf-areafree/playlist.m3u8" {
			t.Errorf("requests[%d]: unexpected query %v", i, q)
		}
	}
}

func TestGetTimeshiftChunklist_MissingSegment(t *testing.T) {
	cases := []struct {
		name string
		drop time.Duration
	}{
		{name: "middle", drop: 10 * time.Minute},
		{name: "last", drop: 30*time.Minute - segmentLength},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeTimefreeServer(t)
			start := fakeProgramStart(t)
			f.drop = start.Add(tc.drop)

			_, err := f.client(t).GetTimeshiftChunklist(context.Background(), fakeStationID, start, false)
			if err == nil || !strings.Contains(err.Error(), "missing segments") {
				t.Errorf("expected a missing segments error, but %v", err)
			}
		})
	}
}

func TestGetTimeshiftChunklist_Unauthorized(t *testing.T) {
	f := newFakeTimefreeServer(t)
	client := f.client(t)
	client.setAuthTokenHeader("")

	_, err := client.GetTimeshiftChunklist(context.Background(), fakeStationID, fakeProgramStart(t), false)
	if err == nil || !strings.Contains(err.Error(), "status 401") {
		t.Errorf("expected a 401 error, but %v", err)
	}
}

func TestGetTimeshiftChunklist_EmptyStationID(t *testing.T) {
	f := newFakeTimefreeServer(t)

	_, err := f.client(t).GetTimeshiftChunklist(context.Background(), "", fakeProgramStart(t), false)
	if err == nil {
		t.Error("Should detect an error.")
	}
}

func TestGetTimeshiftURL(t *testing.T) {
	stationID := "LFR"
	url := GetTimeshiftURL(stationID, time.Now())
	if len(url) == 0 {
		t.Error("A timeshift url is empty.")
	}
}
