package types

// Logger is the logging interface used throughout varc.
//
// Any logging backend (zap, logrus, stdlib log, etc.) can be used
// by providing an adapter that satisfies this interface.
//
// The method names mirror zap.SugaredLogger so the zap adapter
// can be a simple type alias / embedding.
type Logger interface {
	Debugf(format string, args ...any)
	Infof(format string, args ...any)
	Warnf(format string, args ...any)
	Errorf(format string, args ...any)
}

type nopLogger struct{}

func (nopLogger) Debugf(string, ...any) {}
func (nopLogger) Infof(string, ...any)  {}
func (nopLogger) Warnf(string, ...any)  {}
func (nopLogger) Errorf(string, ...any) {}

// NopLogger returns a Logger that discards all messages.
// This is the default for Options.Logger when none is set.
func NopLogger() Logger { return nopLogger{} }
