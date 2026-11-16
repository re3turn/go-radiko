package m3u8

import (
	"bufio"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func readTestData(fileName string) *os.File {
	f, err := os.Open(filepath.Join("..", "..", "testdata", fileName))
	if err != nil {
		panic(err)
	}
	return f
}

func TestGetURI(t *testing.T) {
	expected := "https://radiko.jp/v2/api/ts/chunklist/NejwTOkX.m3u8"

	input := bufio.NewReader(readTestData("uri.m3u8"))
	u, err := GetURI(input)
	if err != nil {
		t.Error(err)
	}
	if u != expected {
		t.Errorf("expected %s, but %s", expected, u)
	}
}

func TestGetSegments(t *testing.T) {
	input := bufio.NewReader(readTestData("chunklist.m3u8"))
	segments, err := GetSegments(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(segments) == 0 {
		t.Fatal("segments is empty.")
	}

	s := segments[1]
	if expected := "http://media.radiko.jp/sound/b/LFR/20161106/20161106_010005_hN47z.aac"; s.URI != expected {
		t.Errorf("expected %s, but %s", expected, s.URI)
	}
	expected := time.Date(2016, 11, 6, 1, 0, 5, 0, time.FixedZone("", 9*60*60))
	if !s.ProgramDateTime.Equal(expected) {
		t.Errorf("expected %s, but %s", expected, s.ProgramDateTime)
	}
	if s.Duration != 5*time.Second {
		t.Errorf("expected 5s, but %s", s.Duration)
	}
}

func TestGetSegments_MasterPlaylist(t *testing.T) {
	input := bufio.NewReader(readTestData("uri.m3u8"))
	if _, err := GetSegments(input); err == nil {
		t.Error("Should detect an error.")
	}
}

func TestGetChunklist(t *testing.T) {
	input := bufio.NewReader(readTestData("chunklist.m3u8"))
	chunklist, err := GetChunklist(input)
	if err != nil {
		t.Error(err)
	}
	if len(chunklist) == 0 {
		t.Error("chunklist is empty.")
	}
}
