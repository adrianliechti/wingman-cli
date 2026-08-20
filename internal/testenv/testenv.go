// Package testenv provides consistent process-environment isolation for tests.
package testenv

import (
	"os"
	"testing"
)

// WingmanHome gives a test an empty, isolated WINGMAN_HOME directory.
func WingmanHome(t testing.TB) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("WINGMAN_HOME", dir)
	return dir
}

// UserHome gives a test an empty, isolated operating-system home directory.
func UserHome(t testing.TB) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
	return dir
}

// RunWithWingmanHome runs a package test suite with isolated Wingman state.
func RunWithWingmanHome(run func() int) int {
	wingmanHome, err := os.MkdirTemp("", "wingman-test-home-")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(wingmanHome)

	os.Setenv("WINGMAN_HOME", wingmanHome)
	return run()
}
