package cmd

import (
	"github.com/spf13/cobra"
	"idefav-proxy/cmd/mgr"
	_ "idefav-proxy/cmd/mgr"
	"idefav-proxy/cmd/proxy"
	"log"
)

var RootCmd = &cobra.Command{
	Use:   "lhotse-agent",
	Short: "idefav proxy是一个代理服务",
	Long:  `Idefav Proxy 是一个高性能代理服务`,
	Run: func(cmd *cobra.Command, args []string) {
		log.Println("idefav proxy service")
	},
}

func Execute() {
	RootCmd.Execute()
}

func init() {
	RootCmd.AddCommand(mgr.ManagerCmd)
	RootCmd.AddCommand(mgr.VersionCmd)
	RootCmd.AddCommand(proxy.ProxyCmd)
}
