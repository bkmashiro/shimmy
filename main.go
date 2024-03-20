package main

import (
	"log"
	"os"
	"strings"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/lambda-feedback/evaluation-function-shim/cmd"
	"github.com/lambda-feedback/evaluation-function-shim/util"
)

var Version string
var Buildtime string
var Commit string

func main() {
	err := setupSentry()
	if err != nil {
		log.Fatalf("sentry init failed: %s", err)
	}

	defer flushSentry()

	appVersion := "local"
	if Version != "" {
		appVersion = Version
	}

	appBuildtime, _ := time.Parse(time.RFC3339, Buildtime)

	cmd.Execute(cmd.ExecuteParams{
		Version:  appVersion,
		Compiled: appBuildtime,
	})
}

func setupSentry() error {
	dsn := os.Getenv("SENTRY_DSN")
	if dsn == "" {
		return nil
	}

	environment := os.Getenv("SENTRY_ENVIRONMENT")
	if environment == "" {
		environment = "local"
	}

	var debug bool
	sentryDebug := strings.ToLower(os.Getenv("SENTRY_DEBUG"))
	if util.Truthy(sentryDebug) {
		debug = true
	}

	err := sentry.Init(sentry.ClientOptions{
		Dsn:              dsn,
		Debug:            debug,
		TracesSampleRate: 1.0,
		EnableTracing:    true,
		Environment:      environment,
		Release:          Commit,
	})
	if err != nil {
		return err
	}

	return nil
}

func flushSentry() {
	// Flush buffered events before the program terminates.
	// Set the timeout to the maximum duration the program can afford to wait.
	defer sentry.Flush(2 * time.Second)
}
