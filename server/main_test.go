package server

import (
	"os"
	"testing"

	"github.com/adrianliechti/wingman-agent/internal/testenv"
)

func TestMain(m *testing.M) {
	os.Exit(testenv.RunWithWingmanHome(m.Run))
}
