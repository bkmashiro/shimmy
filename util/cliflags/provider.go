// Package confmap implements a koanf.Provider that takes a
// cli.Context and provides its flags to koanf.
package cliflags

import (
	"errors"

	"github.com/knadh/koanf/maps"
	"github.com/urfave/cli/v2"
)

// CLIFlags implements a raw map[string]interface{} provider.
type CLIFlags struct {
	mp map[string]interface{}
}

// Provider returns a CLI Provider that takes a CLI context.
// If a delim is provided, it indicates that the keys are flat
// and the map needs to be unflatted by delim.
func Provider(ctx *cli.Context, delim string, cb func(string) string) *CLIFlags {
	// get all visible flags for the root-level app
	flags := ctx.App.VisibleFlags()

	// create a map to store the flag values
	mp := make(map[string]interface{})

	// iterate over the flags and store the values in the map,
	// transforming the flag names if a callback is provided
	for _, flag := range flags {
		name := flag.Names()[0]
		if cb != nil {
			name = cb(name)
		}
		mp[name] = ctx.Value(name)
	}

	// unflatten the map if a delimiter is provided
	// this can happen when `cb` returns a nested key
	if delim != "" {
		mp = maps.Unflatten(mp, delim)
	}

	return &CLIFlags{mp: mp}
}

// ReadBytes is not supported by the confmap provider.
func (e *CLIFlags) ReadBytes() ([]byte, error) {
	return nil, errors.New("cli provider does not support this method")
}

// Read returns the loaded map[string]interface{}.
func (e *CLIFlags) Read() (map[string]interface{}, error) {
	return e.mp, nil
}
