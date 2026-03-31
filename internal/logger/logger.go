package logger

import (
	"os"
	"github.com/rs/zerolog"
)

func New() zerolog.Logger {
	writer := zerolog.ConsoleWriter{Out: os.Stdout}
	return zerolog.New(writer).With().Timestamp().Logger()
}