package proxy

import (
	"github.com/spf13/cobra"
	"idefav-proxy/cmd/mgr"
	"idefav-proxy/cmd/server"
	"idefav-proxy/cmd/upgrade"
	"idefav-proxy/pkg/log"
	"idefav-proxy/pkg/pool"
	"net"
	"net/http"
	"time"
)

type InProxyServer struct {
	Connections map[string]net.Conn
	NumOpen     int32
	IdleTimeOut time.Duration
	ConnPool    pool.Pool
}

func NewInProxyServer() *InProxyServer {
	connPool, _ := NewConnPool("192.168.0.105", 28080, 1, 10000, 10000)
	return &InProxyServer{
		Connections: make(map[string]net.Conn),
		NumOpen:     0,
		IdleTimeOut: 60 * time.Second,
		ConnPool:    connPool,
	}
}

type OutboundServer struct {
	NumOpen     int32
	IdleTimeOut time.Duration
}

func NewOutboundServer() *OutboundServer {
	return &OutboundServer{NumOpen: 0, IdleTimeOut: 60 * time.Second}
}

var ProxyCmd = &cobra.Command{
	Use:   "proxy",
	Short: "proxy server",

	Run: func(cmd *cobra.Command, args []string) {

		iServer := http.Server{
			Handler: mgr.HttpMux,
		}
		var idefavMgrServer = mgr.NewManagementServer(iServer)
		idefavMgrServer.Addr = ":15030"
		server.RegisterServer(idefavMgrServer)

		server.RegisterServer(InboundProxyServer)
		server.RegisterServer(OutboundProxyServer)

		err := server.IdefavServerManager.Startup()
		if err != nil {
			log.Fatal(err)
		}

		upgrade.Ready()

		upgrade.Stop(func() {
			server.IdefavServerManager.Shutdown()
		})
	},
}
