package logger

import (
	"io"
	"os"
	"time"

	"github.com/getsentry/sentry-go"
	sentryzerolog "github.com/getsentry/sentry-go/zerolog"
	"github.com/rs/zerolog"
)

func New() zerolog.Logger {
	writers := []io.Writer{zerolog.ConsoleWriter{Out: os.Stdout}}

	if sentry.CurrentHub().Client() != nil {
		sentryWriter, err := sentryzerolog.NewWithHub(sentry.CurrentHub(), sentryzerolog.Options{
			Levels:          []zerolog.Level{zerolog.ErrorLevel, zerolog.FatalLevel, zerolog.PanicLevel},
			WithBreadcrumbs: true,
			FlushTimeout:    3 * time.Second,
		})
		if err == nil {
			writers = append(writers, sentryWriter)
		}
	}

	return zerolog.New(zerolog.MultiLevelWriter(writers...)).With().Timestamp().Logger()
}
