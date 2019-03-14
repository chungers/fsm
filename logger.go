package fsm // import "github.com/orkestr8/fsm"

type nilLogger struct{}

func (l *nilLogger) Debug(m string, args ...interface{}) {}
func (l *nilLogger) Error(m string, args ...interface{}) {}
func (l *nilLogger) Info(m string, args ...interface{})  {}
