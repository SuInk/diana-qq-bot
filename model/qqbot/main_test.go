package qqbot

import (
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	_ = os.Setenv("DIANA_ALLOW_PRIVATE_HTTP_FETCHES", "true")
	os.Exit(m.Run())
}
