package util_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/lambda-feedback/shimmy/util"
)

func TestTruthy(t *testing.T) {
	tests := []string{"true", "True", "TRUE", "1", "yes", "Yes", "YES"}

	for _, tt := range tests {
		t.Run(tt, func(t *testing.T) {
			actual := util.Truthy(tt)
			assert.True(t, actual)
		})
	}
}

func TestTruthy_False(t *testing.T) {
	tests := []string{"false", "False", "FALSE", "0", "no", "No", "NO", "foo", " ", ""}

	for _, tt := range tests {
		t.Run(tt, func(t *testing.T) {
			actual := util.Truthy(tt)
			assert.False(t, actual)
		})
	}
}
