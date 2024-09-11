package conf

import (
	"fmt"
	"strings"

	"github.com/knadh/koanf/parsers/json"
	"github.com/knadh/koanf/providers/confmap"
	"github.com/knadh/koanf/providers/env"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/v2"
	"github.com/urfave/cli/v2"

	"github.com/lambda-feedback/shimmy/util/cliflags"
)

type ParseOptions struct {
	// Cli is the cli.Context from urfave/cli
	Cli *cli.Context

	// CliMap is a map of cli flag names to config keys
	CliMap map[string]string

	// Defaults is a map of default values
	Defaults DefaultConfig

	// EnvPrefix is the prefix for env vars
	EnvPrefix string

	// FileName is the name of the configuration file to load
	FileName string
}

func Parse[C any](opt ParseOptions) (C, error) {
	k := koanf.New(".")

	if opt.Defaults != nil {
		k.Load(confmap.Provider(opt.Defaults, "."), nil)
	}

	var config C

	if opt.FileName != "" {
		if err := k.Load(file.Provider(opt.FileName), json.Parser()); err != nil {
			return config, fmt.Errorf("error parsing file '%s': %w", opt.FileName, err)
		}
	}

	transformPrefixedEnv := func(s string) string {
		return transformEnv(s, opt.EnvPrefix)
	}

	if err := k.Load(env.Provider(opt.EnvPrefix, ".", transformPrefixedEnv), nil); err != nil {
		return config, fmt.Errorf("error parsing env vars: %w", err)
	}

	if opt.Cli != nil {
		transformFlag := func(s string) string {
			if opt.CliMap != nil {
				if name, ok := opt.CliMap[s]; ok {
					return name
				}
			}

			// replace - with _
			return strings.ReplaceAll(strings.ToLower(s), "-", "_")
		}

		if err := k.Load(cliflags.Provider(opt.Cli, ".", transformFlag), nil); err != nil {
			return config, fmt.Errorf("error parsing cli flags: %w", err)
		}
	}

	if err := k.UnmarshalWithConf("", &config, koanf.UnmarshalConf{Tag: "conf"}); err != nil {
		return config, fmt.Errorf("error unmarshalling config: %w", err)
	}

	return config, nil
}

func transformEnv(s, prefix string) string {
	// allow specifying nested env vars w/ __
	normalized := strings.ReplaceAll(strings.ToLower(s), "__", ".")
	// split normalized env var by separator
	parts := strings.Split(normalized, ".")
	// pop prefix if it is set
	if prefix != "" {
		_, parts = parts[0], parts[1:]
	}
	// create final string
	return strings.Join(parts, ".")
}
