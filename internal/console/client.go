package console

import (
	"fmt"

	"github.com/satcomprojects/gonetem/internal/options"
	"github.com/satcomprojects/gonetem/internal/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type NetemConsoleClient struct {
	Conn   *grpc.ClientConn
	Client proto.NetemClient
}

// Close closes the underlying connection when there is one. It is safe on a
// client built by a test with an injected proto.NetemClient.
func (c *NetemConsoleClient) Close() {
	if c != nil && c.Conn != nil {
		_ = c.Conn.Close()
	}
}

func NewClient(server string) (*NetemConsoleClient, error) {
	var creds = insecure.NewCredentials()
	var opts []grpc.DialOption

	if options.ConsoleConfig.Tls.Enabled {
		var err error

		creds, err = options.LoadConsoleTLSCredentials()
		if err != nil {
			return nil, fmt.Errorf("cannot load TLS credentials: %w", err)
		}
	}
	opts = append(opts, grpc.WithTransportCredentials(creds))

	conn, err := grpc.NewClient(server, opts...)
	if err != nil {
		return nil, err
	}

	return &NetemConsoleClient{
		Conn:   conn,
		Client: proto.NewNetemClient(conn),
	}, nil
}
