package util

import "fmt"

func Must[V any](v V, err error) V {
	if err != nil {
		panic(fmt.Sprintf("util.Must: %v", err))
	}

	return v
}
