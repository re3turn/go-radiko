package radiko

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"time"

	"github.com/yyoshiki41/go-radiko/internal/m3u8"
	"github.com/yyoshiki41/go-radiko/internal/util"
)

const (
	// timeshiftWindow is the longest time range that the timefree playlist
	// endpoint accepts in one request (the "l" parameter, in seconds).
	// Longer ranges are rejected with HTTP 400.
	timeshiftWindow = 300 * time.Second

	// timeshiftOverlap is how far before the collected audio
	// the next window starts. It is about the length of a segment.
	timeshiftOverlap = 5 * time.Second

	// timeshiftGapTolerance is the allowed difference between the end of
	// a segment and the start of the next one.
	// Segment timestamps are off by tens of milliseconds,
	// while a missing segment leaves a gap of about 5 seconds.
	timeshiftGapTolerance = time.Second

	// timeshiftMaxAttempts is how many times in a row a window may fail
	// to advance before GetTimeshiftChunklist gives up.
	timeshiftMaxAttempts = 8
)

// timeshiftRetryInterval is the base wait before requesting a window again.
// The wait grows linearly with the number of attempts.
// It is a variable so that tests can shorten it.
var timeshiftRetryInterval = 500 * time.Millisecond

// TimeshiftPlaylistM3U8 always returns ErrTimeshiftPlaylistM3U8Removed.
//
// Deprecated: radiko no longer serves POST /v2/api/ts/playlist.m3u8,
// and the new timefree endpoint returns at most 300 seconds per playlist.
// Use GetTimeshiftChunklist instead.
func (c *Client) TimeshiftPlaylistM3U8(ctx context.Context, stationID string, start time.Time) (string, error) {
	return "", ErrTimeshiftPlaylistM3U8Removed
}

// GetTimeshiftChunklist returns the urls of all audio segments
// of the program that starts at start.
//
// It requests the timefree playlist in 300-second windows
// and joins the segments, checking that they are contiguous.
// When segments are missing or a request fails with a server or network
// error (or 408 / 429), it requests the same time again,
// giving up after 8 attempts in a row that do not advance.
// The returned segments can be downloaded without the auth token.
//
// Set areafree to true to get a program outside the current area
// (requires a radiko premium login).
// The area-free request has not been verified with a premium account:
// it uses the areafree="1" playlist url with type=c.
func (c *Client) GetTimeshiftChunklist(ctx context.Context, stationID string, start time.Time, areafree bool) ([]string, error) {
	prog, err := c.GetProgramByStartTime(ctx, stationID, start)
	if err != nil {
		return nil, err
	}
	ft, err := util.ParseDatetime(prog.Ft)
	if err != nil {
		return nil, err
	}
	to, err := util.ParseDatetime(prog.To)
	if err != nil {
		return nil, err
	}
	if !to.After(ft) {
		return nil, fmt.Errorf("invalid program time range: %s-%s", prog.Ft, prog.To)
	}

	playlistURL, err := c.timefreePlaylistCreateURL(ctx, stationID, areafree)
	if err != nil {
		return nil, err
	}
	lsid, err := generateLsid()
	if err != nil {
		return nil, err
	}

	streamType := "b"
	if areafree {
		streamType = "c"
	}

	var chunklist []string
	// cursor is the end of the audio collected so far.
	cursor := ft
	// stalls counts consecutive windows that did not advance the cursor.
	stalls := 0
	for cursor.Before(to.Add(-timeshiftGapTolerance)) {
		// A segment that starts slightly before start_at is not returned,
		// so request from a little before the cursor and skip the overlap.
		windowStart := cursor.Add(-timeshiftOverlap).Truncate(time.Second)
		l := timeshiftWindow
		if rest := to.Sub(windowStart); rest < l {
			l = rest
		}

		query := url.Values{}
		query.Set("station_id", stationID)
		query.Set("ft", prog.Ft)
		query.Set("to", prog.To)
		query.Set("start_at", util.Datetime(windowStart))
		query.Set("end_at", prog.To)
		query.Set("l", strconv.Itoa(int(l/time.Second)))
		query.Set("lsid", lsid)
		query.Set("type", streamType)

		segments, err := c.getTimeshiftSegments(ctx, playlistURL+"?"+query.Encode())
		if err != nil && !isRetryable(ctx, err) {
			return nil, fmt.Errorf("failed to get segments at %s: %w", util.Datetime(windowStart), err)
		}

		previous := cursor
		// gapEnd is the start of the first segment after missing segments.
		gapEnd := to
		for _, s := range segments {
			if s.ProgramDateTime.IsZero() || s.Duration <= 0 {
				return nil, errors.New("segment has no EXT-X-PROGRAM-DATE-TIME or duration")
			}
			// The segment belongs to the next program.
			if !s.ProgramDateTime.Before(to.Add(-timeshiftGapTolerance)) {
				break
			}
			end := s.ProgramDateTime.Add(s.Duration)
			// Skip segments already covered by the previous window.
			if !end.After(cursor.Add(timeshiftGapTolerance)) {
				continue
			}
			// Keep the audio up to the gap.
			// The next window requests the missing segments again.
			if s.ProgramDateTime.After(cursor.Add(timeshiftGapTolerance)) {
				gapEnd = s.ProgramDateTime
				break
			}
			chunklist = append(chunklist, s.URI)
			cursor = end
		}

		if cursor.After(previous) {
			stalls = 0
			continue
		}
		stalls++
		if stalls >= timeshiftMaxAttempts {
			if err != nil {
				return nil, fmt.Errorf("failed to get segments at %s: %w", util.Datetime(windowStart), err)
			}
			return nil, fmt.Errorf("missing segments between %s and %s",
				cursor.Format(time.RFC3339Nano), gapEnd.Format(time.RFC3339Nano))
		}
		if err := sleepContext(ctx, time.Duration(stalls)*timeshiftRetryInterval); err != nil {
			return nil, err
		}
	}
	return chunklist, nil
}

// isRetryable reports whether requesting the timefree playlist again
// may succeed after err.
func isRetryable(ctx context.Context, err error) bool {
	if ctx.Err() != nil {
		return false
	}
	var se *statusError
	if errors.As(err, &se) {
		switch {
		case se.code == http.StatusRequestTimeout, se.code == http.StatusTooManyRequests:
			return true
		case se.code >= 400 && se.code < 500:
			// A client error, such as an invalid auth token, does not change on retry.
			return false
		}
	}
	return true
}

func sleepContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// statusError is returned when a response has a status other than 200 OK.
type statusError struct {
	url  string
	code int
}

func (e *statusError) Error() string {
	return fmt.Sprintf("GET %s: status %d", e.url, e.code)
}

// timefreePlaylistCreateURL returns the timefree playlist url of the station.
func (c *Client) timefreePlaylistCreateURL(ctx context.Context, stationID string, areafree bool) (string, error) {
	items, err := c.GetStreamPcHtml5URL(ctx, stationID)
	if err != nil {
		return "", err
	}

	for _, item := range items {
		if item.Timefree && item.Areafree == areafree && item.PlaylistCreateURL != "" {
			return item.PlaylistCreateURL, nil
		}
	}
	return "", fmt.Errorf("timefree playlist url not found for station %s", stationID)
}

// getTimeshiftSegments requests a timefree playlist (master playlist)
// and returns the segments of the media playlist that it points to.
func (c *Client) getTimeshiftSegments(ctx context.Context, playlistURL string) ([]m3u8.Segment, error) {
	resp, err := c.getWithAuthToken(ctx, playlistURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	uri, err := m3u8.GetURI(resp.Body)
	if err != nil {
		return nil, err
	}
	if uri == "" {
		return nil, errors.New("media playlist uri not found")
	}
	ref, err := url.Parse(uri)
	if err != nil {
		return nil, err
	}

	mediaResp, err := c.getWithAuthToken(ctx, resp.Request.URL.ResolveReference(ref).String())
	if err != nil {
		return nil, err
	}
	defer mediaResp.Body.Close()

	return m3u8.GetSegments(mediaResp.Body)
}

func (c *Client) getWithAuthToken(ctx context.Context, u string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set(radikoAuthTokenHeader, c.AuthToken())

	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, &statusError{url: u, code: resp.StatusCode}
	}
	return resp, nil
}

// generateLsid returns a random session id for the timefree playlist.
func generateLsid() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// GetTimeshiftURL returns a timeshift url for web browser.
func GetTimeshiftURL(stationID string, start time.Time) string {
	endpoint := path.Join("#!/ts", stationID, util.Datetime(start))
	return defaultEndpoint + "/" + endpoint
}
