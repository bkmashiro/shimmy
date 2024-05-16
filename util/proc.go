package util

import (
	"os/exec"
	"strconv"
)

func IsProcessAlive(pid int) bool {
	cmd := exec.Command("ps", "-p", strconv.Itoa(pid))

	err := cmd.Run()
	if err, ok := err.(*exec.ExitError); ok {
		// if the process is found ps returns with exit status 0,
		// otherwise it returns with another exit status
		return err.ProcessState.ExitCode() == 0
	}
	if err != nil {
		// if an error occured, return false
		return false
	}

	// ps returned a zero exit status, so the process was found
	return true
}
