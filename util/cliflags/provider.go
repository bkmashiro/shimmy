// Package confmap implements a koanf.Provider that takes a
// cli.Context and provides its flags to koanf.
package cliflags

import (
	"errors"
	"fmt"

	"github.com/knadh/koanf/maps"
	"github.com/urfave/cli/v2"
)

// CLIFlags implements a raw map[string]any provider.
type CLIFlags struct {
	mp map[string]any
}

// Provider returns a CLI Provider that takes a CLI context.
// If a delim is provided, it indicates that the keys are flat
// and the map needs to be unflatted by delim.
func Provider(ctx *cli.Context, delim string, cb func(string) string) *CLIFlags {
	// get all visible flags for the root-level app
	appFlags := ctx.App.VisibleFlags()
	commandFlags := ctx.Command.VisibleFlags()

	flags := map[string]cli.Flag{}
	for _, flag := range appFlags {
		flags[flag.Names()[0]] = flag
	}
	for _, flag := range commandFlags {
		flags[flag.Names()[0]] = flag
	}

	flagNames := ctx.FlagNames()

	// create a map to store the flag values
	mp := make(map[string]any)

	// iterate over the flags and store the values in the map,
	// transforming the flag names if a callback is provided
	for _, flagName := range flagNames {
		flag, ok := flags[flagName]
		if !ok {
			continue
		}

		value, err := getFlagValue(ctx, flag)
		if err != nil {
			continue
		}

		var mapName = flagName
		if cb != nil {
			mapName = cb(flagName)
		}
		mp[mapName] = value
	}

	// unflatten the map if a delimiter is provided
	// this can happen when `cb` returns a nested key
	if delim != "" {
		mp = maps.Unflatten(mp, delim)
	}

	fmt.Printf("mp: %v\n", mp)

	return &CLIFlags{mp: mp}
}

// ReadBytes is not supported by the confmap provider.
func (e *CLIFlags) ReadBytes() ([]byte, error) {
	return nil, errors.New("cli provider does not support this method")
}

// Read returns the loaded map[string]any.
func (e *CLIFlags) Read() (map[string]any, error) {
	return e.mp, nil
}

func getFlagValue(ctx *cli.Context, flag cli.Flag) (any, error) {
	name := flag.Names()[0]

	if _, ok := flag.(*cli.StringFlag); ok {
		return ctx.String(name), nil
	} else if _, ok := flag.(*cli.StringSliceFlag); ok {
		return ctx.StringSlice(name), nil
	} else if _, ok := flag.(*cli.PathFlag); ok {
		return ctx.Path(name), nil
	} else if _, ok := flag.(*cli.IntFlag); ok {
		return ctx.Int(name), nil
	} else if _, ok := flag.(*cli.IntSliceFlag); ok {
		return ctx.IntSlice(name), nil
	} else if _, ok := flag.(*cli.Int64Flag); ok {
		return ctx.Int64(name), nil
	} else if _, ok := flag.(*cli.Int64SliceFlag); ok {
		return ctx.Int64Slice(name), nil
	} else if _, ok := flag.(*cli.BoolFlag); ok {
		return ctx.Bool(name), nil
	} else if _, ok := flag.(*cli.Float64Flag); ok {
		return ctx.Float64(name), nil
	} else if _, ok := flag.(*cli.Float64SliceFlag); ok {
		return ctx.Float64Slice(name), nil
	} else if f, ok := flag.(*cli.DurationFlag); ok {
		return f.Value, nil
	}

	return nil, fmt.Errorf("unsupported flag type %T", flag)
}
