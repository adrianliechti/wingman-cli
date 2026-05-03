package system

import (
	"fmt"
	"net"
)

// FreePort returns a free port on localhost. It first tries the preferred
// port; if that is already in use it asks the OS to assign one by listening
// on :0 and reads the port back from the resulting address.
func FreePort(preferred int) (int, error) {
	ln, err := net.Listen("tcp", fmt.Sprintf("localhost:%d", preferred))
	if err != nil {
		ln, err = net.Listen("tcp", "localhost:0")
		if err != nil {
			return 0, err
		}
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port, nil
}
