package observe

import (
	"os"
	"runtime/debug"
	"strconv"
	"time"

	"github.com/getsentry/sentry-go"
)

func InitSentry() (bool, error) {
	dsn := os.Getenv("SENTRY_DSN")
	if dsn == "" {
		return false, nil
	}

	environment := os.Getenv("SENTRY_ENVIRONMENT")
	if environment == "" {
		environment = "development"
	}

	tracesSampleRate := 0.0
	if raw := os.Getenv("SENTRY_TRACES_SAMPLE_RATE"); raw != "" {
		parsed, err := strconv.ParseFloat(raw, 64)
		if err == nil {
			tracesSampleRate = parsed
		}
	}

	err := sentry.Init(sentry.ClientOptions{
		Dsn:              dsn,
		Environment:      environment,
		Release:          release(),
		EnableTracing:    tracesSampleRate > 0,
		TracesSampleRate: tracesSampleRate,
	})
	if err != nil {
		return false, err
	}

	return true, nil
}

func SentryEnabled() bool {
	return sentry.CurrentHub().Client() != nil
}

func FlushSentry() {
	sentry.Flush(2 * time.Second)
}

type CronHeartbeat struct {
	slug string
	last time.Time
}

func NewCronHeartbeat(slug string) *CronHeartbeat {
	return &CronHeartbeat{slug: slug}
}

func (h *CronHeartbeat) Beat() {
	if !SentryEnabled() {
		return
	}
	if time.Since(h.last) < time.Minute {
		return
	}
	h.last = time.Now()
	sentry.CaptureCheckIn(
		&sentry.CheckIn{
			MonitorSlug: h.slug,
			Status:      sentry.CheckInStatusOK,
		},
		&sentry.MonitorConfig{
			Schedule:              sentry.IntervalSchedule(1, sentry.MonitorScheduleUnitMinute),
			CheckInMargin:         2,
			MaxRuntime:            2,
			FailureIssueThreshold: 2,
			RecoveryThreshold:     1,
		},
	)
}

func CapturePanic() {
	r := recover()
	if r == nil {
		return
	}
	sentry.CurrentHub().Recover(r)
	sentry.Flush(2 * time.Second)
	panic(r)
}

func release() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	for _, setting := range info.Settings {
		if setting.Key == "vcs.revision" {
			return setting.Value
		}
	}
	return ""
}
