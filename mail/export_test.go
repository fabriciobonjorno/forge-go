package mail

import (
	"context"
	"net"
)

// ClassifyForTest exposes error classification to the external tests.
func ClassifyForTest(s *SMTPSender, ctx context.Context, stage string, err error) error {
	return s.classify(ctx, stage, err)
}

// SetDialerForTest redirects the sender's connection, so a test can present
// a host name that is not loopback while talking to a local fake server.
func SetDialerForTest(s *SMTPSender, dial func(ctx context.Context, network, address string) (net.Conn, error)) {
	s.dialer = dial
}
