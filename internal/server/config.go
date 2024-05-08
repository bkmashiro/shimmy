package server

type HttpConfig struct {
	Host string `conf:"host"`
	Port int    `conf:"port"`
	H2c  bool   `conf:"h2c"`
}
