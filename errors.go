package radiko

import "errors"

var (
	// ErrProgramNotFound is returned when a program not found
	ErrProgramNotFound = errors.New("program not found")

	// ErrTimeshiftPlaylistM3U8Removed is returned by TimeshiftPlaylistM3U8
	// because radiko no longer serves the playlist.
	ErrTimeshiftPlaylistM3U8Removed = errors.New("radiko no longer serves /v2/api/ts/playlist.m3u8, use GetTimeshiftChunklist")
)
