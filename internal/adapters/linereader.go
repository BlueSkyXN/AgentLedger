package adapters

import (
	"bufio"
	"errors"
	"fmt"
	"io"
)

const (
	// Source lines can contain unbounded tool output or attachments. Cap the
	// retained content and discard the remainder without buffering it in full.
	maxJSONLLineBytes  = 64 * 1024 * 1024
	jsonlReadChunkSize = 64 * 1024
)

// jsonlMaxLineBytes is the limit used by adapters; tests lower it to exercise
// the oversized-line path without multi-megabyte fixtures.
var jsonlMaxLineBytes = maxJSONLLineBytes

// jsonlLineReader reads newline-delimited records like bufio.Scanner with
// ScanLines, except that a line longer than its limit does not abort the rest
// of the file: Scan still reports the line (so callers keep counting line
// numbers exactly as before), Bytes returns an empty slice, and the line is
// discarded without being buffered in full.
type jsonlLineReader struct {
	reader    *bufio.Reader
	maxLine   int
	line      []byte
	oversized bool
	skipped   int
	err       error
	done      bool
}

func newJSONLLineReader(r io.Reader) *jsonlLineReader {
	return newJSONLLineReaderWithLimit(r, jsonlMaxLineBytes)
}

func newJSONLLineReaderWithLimit(r io.Reader, maxLine int) *jsonlLineReader {
	return &jsonlLineReader{reader: bufio.NewReaderSize(r, jsonlReadChunkSize), maxLine: maxLine}
}

// Scan advances to the next line. It returns false at EOF or on a read error.
func (l *jsonlLineReader) Scan() bool {
	if l.done {
		return false
	}
	l.line = l.line[:0]
	l.oversized = false
	sawData := false
	for {
		chunk, err := l.reader.ReadSlice('\n')
		if len(chunk) > 0 {
			sawData = true
			if !l.oversized {
				contentLen := len(l.line) + len(chunk)
				if err == nil { // chunk ends with the newline, which is not content
					contentLen--
				}
				if contentLen > l.maxLine {
					l.oversized = true
					l.line = l.line[:0]
				} else {
					l.line = append(l.line, chunk...)
				}
			}
		}
		switch {
		case err == nil:
			return l.finishLine()
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		case errors.Is(err, io.EOF):
			l.done = true
			if !sawData {
				return false
			}
			return l.finishLine()
		default:
			l.done = true
			l.err = err
			return false
		}
	}
}

func (l *jsonlLineReader) finishLine() bool {
	if l.oversized {
		l.skipped++
		l.line = l.line[:0]
		return true
	}
	if n := len(l.line); n > 0 && l.line[n-1] == '\n' {
		l.line = l.line[:n-1]
	}
	if n := len(l.line); n > 0 && l.line[n-1] == '\r' {
		l.line = l.line[:n-1]
	}
	return true
}

// Bytes returns the current line without its line ending. The slice is only
// valid until the next call to Scan. It is empty for an oversized line.
func (l *jsonlLineReader) Bytes() []byte {
	return l.line
}

// Oversized reports whether the current line exceeded the limit and was dropped.
func (l *jsonlLineReader) Oversized() bool {
	return l.oversized
}

// SkippedLines returns how many oversized lines were dropped so far.
func (l *jsonlLineReader) SkippedLines() int {
	return l.skipped
}

func (l *jsonlLineReader) Err() error {
	return l.err
}

// oversizedLineWarnings returns a privacy-safe parse warning for dropped lines.
func oversizedLineWarnings(skipped int) []string {
	if skipped <= 0 {
		return nil
	}
	return []string{fmt.Sprintf("skipped %d oversized line(s) longer than %d bytes", skipped, jsonlMaxLineBytes)}
}
