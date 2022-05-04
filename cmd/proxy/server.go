package proxy

import (
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"lhotse-agent/cmd/mgr"
	"lhotse-agent/cmd/proxy/config"
	"lhotse-agent/cmd/proxy/constants"
	"lhotse-agent/cmd/proxy/data"
	"lhotse-agent/cmd/server"
	"lhotse-agent/cmd/upgrade"
	"lhotse-agent/pkg/log"
	"lhotse-agent/pkg/pool"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

type InProxyServer struct {
	Connections map[string]net.Conn
	NumOpen     int32
	IdleTimeOut time.Duration
	ConnPool    pool.Pool
	Cfg         *config.Config
	Port        int32
}

func NewInProxyServer(idleTimeOut time.Duration, port int32, cfg *config.Config) *InProxyServer {
	return &InProxyServer{
		Connections: make(map[string]net.Conn),
		NumOpen:     0,
		IdleTimeOut: idleTimeOut,
		Port:        port,
		ConnPool:    nil,
		Cfg:         cfg,
	}
}

type OutboundServer struct {
	NumOpen     int32
	IdleTimeOut time.Duration
	Port        int32
	Cfg         *config.Config
}

func NewOutboundServer(idleTimeOut time.Duration, port int32, cfg *config.Config) *OutboundServer {
	return &OutboundServer{NumOpen: 0, IdleTimeOut: idleTimeOut, Port: port, Cfg: cfg}
}

var ProxyCmd = &cobra.Command{
	Use:    "proxy",
	Short:  "proxy server",
	PreRun: bindFlags,
	Run: func(cmd *cobra.Command, args []string) {
		cfg := constructCfg()
		log.Infof(cfg)

		data.ServiceData.LoadServiceData(cfg.FileName)

		// proxy 管理服务
		mgrServer := http.Server{
			Handler: mgr.HttpMux,
		}
		server.RegisterServer(mgr.NewManagementServer(mgrServer, ":"+strconv.Itoa(int(cfg.ProxyMgrPort))))
		server.RegisterServer(NewInProxyServer(cfg.ConnIdleTimeOut, cfg.InBoundProxyPort, cfg))
		server.RegisterServer(NewOutboundServer(cfg.ConnIdleTimeOut, cfg.OutBoundProxyPort, cfg))

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

func constructCfg() *config.Config {
	cfg := &config.Config{
		ServerName:        viper.GetString(constants.ServerName),
		FileName:          viper.GetString(constants.FileName),
		ProxyMgrPort:      viper.GetInt32(constants.ProxyMgrPort),
		InBoundProxyPort:  viper.GetInt32(constants.InBoundProxyPort),
		OutBoundProxyPort: viper.GetInt32(constants.OutBoundProxyPort),
		ConnIdleTimeOut:   viper.GetDuration(constants.ConnIdleTimeOut),
	}
	return cfg
}

func bindFlags(cmd *cobra.Command, args []string) {
	// Read in all environment variables
	viper.AutomaticEnv()
	// Replace - with _; so that environment variables are looked up correctly.
	viper.SetEnvKeyReplacer(strings.NewReplacer("-", "_"))

	if err := viper.BindPFlag(constants.ServerName, cmd.Flags().Lookup(constants.ServerName)); err != nil {
		handleError(err)
	}

	if err := viper.BindPFlag(constants.FileName, cmd.Flags().Lookup(constants.FileName)); err != nil {
		handleError(err)
	}

	if err := viper.BindPFlag(constants.ProxyMgrPort, cmd.Flags().Lookup(constants.ProxyMgrPort)); err != nil {
		handleError(err)
	}

	if err := viper.BindPFlag(constants.InBoundProxyPort, cmd.Flags().Lookup(constants.InBoundProxyPort)); err != nil {
		handleError(err)
	}

	if err := viper.BindPFlag(constants.OutBoundProxyPort, cmd.Flags().Lookup(constants.OutBoundProxyPort)); err != nil {
		handleError(err)
	}

	if err := viper.BindPFlag(constants.ConnIdleTimeOut, cmd.Flags().Lookup(constants.ConnIdleTimeOut)); err != nil {
		handleError(err)
	}
}

func handleError(err error) {
	handleErrorWithCode(err, 1)
}

func handleErrorWithCode(err error, code int) {
	log.Error(err)
	os.Exit(code)
}

func bindCmdFlags(rootCmd *cobra.Command) {
	rootCmd.Flags().StringP(constants.ServerName, "s", "Lhotse Proxy", "服务器名称")
	rootCmd.Flags().StringP(constants.FileName, "c", "config.yaml", "配置文件名称")
	rootCmd.Flags().Int32P(constants.ProxyMgrPort, "m", 15030, "Proxy服务管理端口")
	rootCmd.Flags().Int32P(constants.InBoundProxyPort, "i", 15006, "Proxy服务入口流量代理端口")
	rootCmd.Flags().Int32P(constants.OutBoundProxyPort, "o", 15001, "Proxy服务出口流量代理端口")
	rootCmd.Flags().Duration(constants.ConnIdleTimeOut, 60*time.Second, "空闲链接默认超时时间")
}

func init() {
	bindCmdFlags(ProxyCmd)
}
