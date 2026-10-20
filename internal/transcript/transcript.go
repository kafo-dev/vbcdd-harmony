// Package transcript stores one conversation as JSON Lines: one message per
// line, so a run picks up where the last one stopped.
package transcript

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"charm.land/fantasy"
)

// The permissions of the file and the directory that holds it.
const (
	fileMode = 0o600
	dirMode  = 0o700
)

// maxLineBytes bounds one stored message. A conversation carrying a pasted
// file stays well inside it, and the bound keeps a damaged file from being read
// into memory whole.
const maxLineBytes = 8 << 20

// File is one conversation on disk.
type File struct {
	// Path is the conversation file.
	Path string
}

// Load reads the conversation. A file that does not exist is an empty
// conversation.
//
// A final line that does not parse is dropped, because a process that dies
// mid-write leaves one; any other damage is an error, since it means the file
// is not what this package wrote.
func (f File) Load() ([]fantasy.Message, error) {
	file, err := os.Open(f.Path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer file.Close()

	var (
		messages []fantasy.Message
		lines    []string
		scanner  = bufio.NewScanner(file)
	)
	scanner.Buffer(make([]byte, 0, bufio.MaxScanTokenSize), maxLineBytes)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if err = scanner.Err(); err != nil {
		return nil, err
	}
	for i, line := range lines {
		if line == "" {
			continue
		}
		var message fantasy.Message
		err = json.Unmarshal([]byte(line), &message)
		if err != nil {
			if i == len(lines)-1 {
				break
			}
			return nil, fmt.Errorf("%s: line %d: %w", f.Path, i+1, err)
		}
		messages = append(messages, message)
	}
	return messages, nil
}

// Append adds messages to the conversation, one JSON object per line.
func (f File) Append(messages ...fantasy.Message) error {
	if len(messages) == 0 {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(f.Path), dirMode); err != nil {
		return err
	}
	file, err := os.OpenFile(f.Path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, fileMode)
	if err != nil {
		return err
	}
	defer file.Close()
	encoder := json.NewEncoder(file)
	for _, message := range messages {
		if err = encoder.Encode(message); err != nil {
			return err
		}
	}
	return nil
}

// Reset discards the conversation.
func (f File) Reset() error {
	if err := os.Remove(f.Path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// Size reports the size of the stored conversation, in bytes. A conversation
// that was never written is empty.
func (f File) Size() (int64, error) {
	info, err := os.Stat(f.Path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	return info.Size(), nil
}
