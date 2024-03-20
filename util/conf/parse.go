package conf

import (
	"strings"

	"github.com/knadh/koanf/parsers/dotenv"
	"github.com/knadh/koanf/parsers/json"
	"github.com/knadh/koanf/providers/confmap"
	"github.com/knadh/koanf/providers/env"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/v2"
	"github.com/lambda-feedback/shimmy/util/cliflags"
	"github.com/urfave/cli/v2"
	"go.uber.org/zap"
)

type ParseOptions struct {
	CLI      *cli.Context
	Defaults DefaultConfig
	Prefix   string
	FileName string
	Log      *zap.Logger
}

func Parse[C any](opt ParseOptions) (*C, error) {
	var log *zap.Logger
	if opt.Log != nil {
		log = opt.Log
	} else {
		log = zap.NewNop()
	}

	k := koanf.New(".")

	k.Load(confmap.Provider(opt.Defaults, "."), nil)

	if opt.FileName != "" {
		if err := k.Load(file.Provider(opt.FileName), json.Parser()); err != nil {
			log.With(zap.Error(err), zap.String("file", opt.FileName)).
				Error("error parsing file")
		}
	}

	dotenvParser := dotenv.ParserEnv(opt.Prefix, ".", transformEnv)

	if err := k.Load(file.Provider(".env"), dotenvParser); err != nil {
		log.Debug(".env not found", zap.Error(err))
	}

	if err := k.Load(env.Provider(opt.Prefix, ".", transformEnv), nil); err != nil {
		log.Error("error parsing env vars", zap.Error(err))
		return nil, err
	}

	if opt.CLI != nil {
		if err := k.Load(cliflags.Provider(opt.CLI, ".", transformFlag), nil); err != nil {
			log.Error("error parsing cli flags", zap.Error(err))
			return nil, err
		}
	}

	var config C

	if err := k.UnmarshalWithConf("", &config, koanf.UnmarshalConf{Tag: "conf"}); err != nil {
		log.Error("error unmarshalling config", zap.Error(err))
		return nil, err
	}

	return &config, nil
}

func transformEnv(s string) string {
	// allow specifying nested env vars w/ __
	normalized := strings.ReplaceAll(strings.ToLower(s), "__", ".")
	// split normalized env var by separator
	parts := strings.Split(normalized, ".")
	// pop prefix
	_, parts = parts[0], parts[1:]
	// create final string
	return strings.Join(parts, ".")
}

func transformFlag(s string) string {
	// replace - with _
	return strings.ReplaceAll(strings.ToLower(s), "-", "_")
}
