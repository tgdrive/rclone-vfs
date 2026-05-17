// Package vfscommon provides utilities for VFS.
package types

import (
	"encoding/json"
	"fmt"
	"strings"
)

type cacheModeChoices struct{}

func (cacheModeChoices) Choices() []string {
	return []string{
		CacheModeOff:     "off",
		CacheModeMinimal: "minimal",
		CacheModeWrites:  "writes",
		CacheModeFull:    "full",
	}
}

// CacheMode controls the functionality of the cache
type CacheMode int

// CacheMode options
const (
	CacheModeOff     CacheMode = iota // cache nothing - return errors for writes which can't be satisfied
	CacheModeMinimal                  // cache only the minimum, e.g. read/write opens
	CacheModeWrites                   // cache all files opened with write intent
	CacheModeFull                     // cache all files opened in any mode
)

// String renders the CacheMode as a string
func (e CacheMode) String() string {
	var c cacheModeChoices
	choices := c.Choices()
	if int(e) >= len(choices) {
		return fmt.Sprintf("Unknown(%d)", e)
	}
	return choices[e]
}

// Choices returns the possible values of the CacheMode.
func (e CacheMode) Choices() []string {
	var c cacheModeChoices
	return c.Choices()
}

// Help returns a comma separated list of all possible states.
func (e CacheMode) Help() string {
	return strings.Join(e.Choices(), ", ")
}

// Type returns the type of the value for pflag
func (e CacheMode) Type() string {
	return "CacheMode"
}

// Set the CacheMode entries
func (e *CacheMode) Set(s string) error {
	for i, choice := range e.Choices() {
		if strings.EqualFold(s, choice) {
			*e = CacheMode(i)
			return nil
		}
	}
	return fmt.Errorf("invalid choice %q from: %s", s, e.Help())
}

// Type of the value
func (cacheModeChoices) Type() string {
	return "CacheMode"
}

// Scan implements the fmt.Scanner interface
func (e *CacheMode) Scan(s fmt.ScanState, ch rune) error {
	token, err := s.Token(true, nil)
	if err != nil {
		return err
	}
	return e.Set(string(token))
}

// UnmarshalJSON parses it as a string or an integer
func (e *CacheMode) UnmarshalJSON(in []byte) error {
	choices := e.Choices()
	// Try to parse as string first
	var str string
	err := json.Unmarshal(in, &str)
	if err == nil {
		return e.Set(str)
	}
	// If that fails parse as integer
	var i int64
	err = json.Unmarshal(in, &i)
	if err != nil {
		return err
	}
	if i < 0 || i >= int64(len(choices)) {
		return fmt.Errorf("%d is out of range: must be 0..%d", i, len(choices))
	}
	*e = CacheMode(i)
	return nil
}

// MarshalJSON encodes it as string
func (e CacheMode) MarshalJSON() ([]byte, error) {
	return json.Marshal(e.String())
}
