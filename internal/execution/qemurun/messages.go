package qemurun

const ProtocolVersion = 1

type Mode string

const (
	ModeFile Mode = "file"
	ModeRPC  Mode = "rpc"
)

type HelloMessage struct {
	Version int `json:"version"`
}

type ReadyMessage struct {
	Version int    `json:"version"`
	BootID  string `json:"boot_id"`
}

type StartMessage struct {
	Mode          Mode     `json:"mode"`
	Command       string   `json:"command"`
	Args          []string `json:"args,omitempty"`
	Cwd           string   `json:"cwd,omitempty"`
	Env           []string `json:"env,omitempty"`
	Transport     string   `json:"transport,omitempty"`
	Endpoint      string   `json:"endpoint,omitempty"`
	GuestEndpoint string   `json:"guest_endpoint,omitempty"`
}

type FileResultMessage struct {
	Response []byte `json:"response,omitempty"`
	Stdout   string `json:"stdout,omitempty"`
	Stderr   string `json:"stderr,omitempty"`
	ExitCode int    `json:"exit_code"`
	Error    string `json:"error,omitempty"`
}

type ExitMessage struct {
	Code  int    `json:"code"`
	Error string `json:"error,omitempty"`
}
