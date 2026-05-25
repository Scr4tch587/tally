package logger

import (
	"github.com/rs/zerolog"
	"os"
)

func New() zerolog.Logger {
	writer := zerolog.ConsoleWriter{Out: os.Stdout}
	return zerolog.New(writer).With().Timestamp().Logger()
}
