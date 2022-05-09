package mgr

import (
	"fmt"
	"github.com/spf13/cobra"
	"golang.org/x/net/context"
	cleanCmd "lhotse-agent/clean-iptables/pkg/cmd"
	cleanConfig "lhotse-agent/clean-iptables/pkg/config"
	"lhotse-agent/cmd/proxy/config"
	"lhotse-agent/cmd/server"
	"lhotse-agent/cmd/upgrade"
	"lhotse-agent/iptables/pkg/capture"
	iptableConfig "lhotse-agent/iptables/pkg/config"
	"lhotse-agent/iptables/pkg/constants"
	dep "lhotse-agent/iptables/pkg/dependencies"
	"lhotse-agent/iptables/pkg/validation"
	"lhotse-agent/pkg/env"
	idefavLog "lhotse-agent/pkg/log"
	"lhotse-agent/util"
	"log"
	"net"
	"net/http"
	"net/http/pprof"
	"time"
)

type ManagementServer struct {
	Server http.Server
	cfg    *config.Config
	Addr   string
}

func NewManagementServer(server http.Server, addr string) *ManagementServer {
	return &ManagementServer{Server: server, Addr: addr}
}

func (m *ManagementServer) Startup() error {
	ln, err := upgrade.Upgrade.Listen("tcp", m.Addr)
	if err != nil {
		return err
	}

	util.GO(func() {
		err = m.Server.Serve(ln)
		if err != http.ErrServerClosed {
			log.Println("HTTP pserver:", err)
		}
	})

	return err
}

func (m *ManagementServer) Shutdown() error {
	return m.Server.Shutdown(context.Background())
}

var HttpMux *http.ServeMux

func init() {

	HttpMux = http.NewServeMux()
	HttpMux.HandleFunc("/debug/pprof/", pprof.Index)
	HttpMux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	HttpMux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	HttpMux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	HttpMux.HandleFunc("/debug/pprof/trace", pprof.Trace)

	HttpMux.HandleFunc("/version", func(rw http.ResponseWriter, r *http.Request) {
		//log.Println(version)
		rw.Write([]byte(Version + "\n"))
	})

	HttpMux.HandleFunc("/demo", func(rw http.ResponseWriter, r *http.Request) {
		//log.Println(version)
		time.Sleep(100)
		rw.Write([]byte(Version + "\n"))
	})

	HttpMux.HandleFunc("/upgrade", func(rw http.ResponseWriter, r *http.Request) {
		log.Println("upgraded")
		err := upgrade.Upgrade.Upgrade()
		if err != nil {
			log.Println("Upgrade failed:", err)
			rw.Write([]byte("upgraded failed:" + err.Error()))
		} else {
			rw.Write([]byte("upgraded success"))
		}

	})

	HttpMux.HandleFunc("/download", func(writer http.ResponseWriter, request *http.Request) {
		request.ParseForm()
		ver := request.Form.Get("ver")
		err := Download("lhotse-agent", ver)
		if err != nil {
			writer.Write([]byte(err.Error()))
			return
		}
		writer.Write([]byte("更新成功"))
	})

	HttpMux.HandleFunc("/clean-iptables", func(writer http.ResponseWriter, request *http.Request) {
		cfg := &cleanConfig.Config{
			DryRun:             false,
			ProxyUID:           "",
			ProxyGID:           "",
			RedirectDNS:        false,
			DNSServersV4:       nil,
			DNSServersV6:       nil,
			CaptureAllDNS:      false,
			OwnerGroupsInclude: "*",
			OwnerGroupsExclude: "",
		}
		if err := cfg.Validate(); err != nil {
			writer.Write([]byte(err.Error()))
			return
		}
		ext := cleanCmd.NewDependencies(cfg)
		cleaner := cleanCmd.NewIptablesCleaner(cfg, ext)
		cleaner.Run()
		writer.Write([]byte("操作成功"))
	})

	HttpMux.HandleFunc("/iptables", func(writer http.ResponseWriter, request *http.Request) {
		envoyUserVar := env.RegisterStringVar(constants.EnvoyUser, "lhotse-agent", "Envoy proxy username")
		cfg := &iptableConfig.Config{
			DryRun:                  false,
			TraceLogging:            false,
			RestoreFormat:           false,
			ProxyPort:               "15001",
			InboundCapturePort:      "15006",
			InboundTunnelPort:       "15008",
			ProxyUID:                envoyUserVar.Get(),
			ProxyGID:                envoyUserVar.Get(),
			InboundInterceptionMode: "",
			InboundTProxyMark:       "1337",
			InboundTProxyRouteTable: "133",
			InboundPortsInclude:     "*",
			InboundPortsExclude:     "18080,22,15030",
			OwnerGroupsInclude:      "*",
			OwnerGroupsExclude:      "",
			OutboundPortsInclude:    "",
			OutboundPortsExclude:    "28080,22",
			OutboundIPRangesInclude: "*",
			OutboundIPRangesExclude: "",
			KubeVirtInterfaces:      "",
			ExcludeInterfaces:       "",
			IptablesProbePort:       uint16(15002),
			ProbeTimeout:            5 * time.Second,
			SkipRuleApply:           false,
			RunValidation:           false,
			RedirectDNS:             false,
			DropInvalid:             false,
			CaptureAllDNS:           false,
			OutputPath:              "",
			NetworkNamespace:        "",
			CNIMode:                 false,
		}
		if err := cfg.Validate(); err != nil {
			writer.Write([]byte(err.Error()))
			return
		}
		var ext dep.Dependencies
		if cfg.DryRun {
			ext = &dep.StdoutStubDependencies{}
		} else {
			ext = &dep.RealDependencies{
				CNIMode:          cfg.CNIMode,
				NetworkNamespace: cfg.NetworkNamespace,
			}
		}

		iptConfigurator := capture.NewIptablesConfigurator(cfg, ext)
		if !cfg.SkipRuleApply {
			iptConfigurator.Run()
			if err := capture.ConfigureRoutes(cfg, ext); err != nil {
				idefavLog.Errorf("failed to configure routes: ")
				writer.Write([]byte(err.Error()))
				return
			}
		}
		if cfg.RunValidation {
			hostIP, err := getLocalIP()
			if err != nil {
				// Assume it is not handled by istio-cni and won't reuse the ValidationErrorCode
				panic(err)
			}
			validator := validation.NewValidator(cfg, hostIP)

			if err := validator.Run(); err != nil {
				writer.Write([]byte(err.Error()))
				return
			}
		}
		writer.Write([]byte("操作成功"))
	})

}

var ManagerCmd = &cobra.Command{
	Use:   "mgr",
	Short: "Manage Proxy Server",

	Run: func(cmd *cobra.Command, args []string) {
		iServer := http.Server{
			Handler: HttpMux,
		}
		var idefavMgrServer = NewManagementServer(iServer, ":18080")
		server.RegisterServer(idefavMgrServer)

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

func getLocalIP() (net.IP, error) {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil, err
	}

	for _, a := range addrs {
		if ipnet, ok := a.(*net.IPNet); ok && !ipnet.IP.IsLoopback() && !ipnet.IP.IsLinkLocalUnicast() && !ipnet.IP.IsLinkLocalMulticast() {
			return ipnet.IP, nil
		}
	}
	return nil, fmt.Errorf("no valid local IP address found")
}
