package types

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// FileMode is a command line friendly os.FileMode
type FileMode os.FileMode

// String turns FileMode into a string
func (x FileMode) String() string {
	return fmt.Sprintf("%03o", x)
}

// Set a FileMode
func (x *FileMode) Set(s string) error {
	i, err := strconv.ParseInt(s, 8, 32)
	if err != nil {
		return fmt.Errorf("bad FileMode - must be octal digits: %w", err)
	}
	*x = (FileMode)(i)
	return nil
}

// Type of the value
func (x FileMode) Type() string {
	return "FileMode"
}

// UnmarshalJSON makes sure the value can be parsed as a string or integer in JSON
func (x *FileMode) UnmarshalJSON(in []byte) error {
	s := strings.Trim(string(in), `"`)
	i, err := strconv.ParseInt(s, 0, 64)
	if err != nil {
		return fmt.Errorf("bad FileMode: %w", err)
	}
	*x = FileMode(i)
	return nil
}
