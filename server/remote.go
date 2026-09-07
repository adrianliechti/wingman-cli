package server

import (
	"errors"
	"fmt"
	"os"

	"github.com/adrianliechti/wingman-agent/pkg/remote"
)

// Remote access is another transport for the same handler. It has no session
// state of its own and shares the workspace's instance checks and lifetime.
// Called during New, before the Server can be concurrently closed.
func (s *Server) startRemote(opts remote.ClientOptions) error {
	if opts.Token == "" {
		return errors.New("remote access requires --remote-token or WINGMAN_REMOTE_TOKEN")
	}
	opts.Credentials = remote.NewCredentials()
	pair, err := remote.PairURL(opts.Relay, opts.Credentials)
	if err != nil {
		return err
	}
	printed := false
	opts.OnStatus = func(connected bool, err error) {
		if connected {
			if !printed {
				fmt.Fprintf(os.Stderr, "\nRemote access: %s\n\n%s\n", pair, remote.QRCode(pair))
				printed = true
			}
			fmt.Fprintln(os.Stderr, "Remote access connected")
		} else if err != nil {
			fmt.Fprintf(os.Stderr, "Remote connection interrupted: %v\n", err)
		}
	}
	s.background.Go(func() {
		if err := remote.Serve(s.ctx, opts, s); err != nil {
			fmt.Fprintf(os.Stderr, "Remote access stopped: %v\n", err)
		}
	})
	return nil
}
