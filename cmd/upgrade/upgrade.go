package upgrade

import (
	"fmt"
	"github.com/cloudflare/tableflip"
	"lhotse-agent/util"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"
)

var Upgrade *tableflip.Upgrader

func init() {
	upg, err := tableflip.New(tableflip.Options{})
	if err != nil {
		panic(err)
	}
	Upgrade = upg
	//defer Upgrade.Stop()
	//
	//time.Sleep(time.Second)

	log.SetPrefix(fmt.Sprintf("[PID: %d] ", os.Getpid()))

	util.GO(func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGHUP)
		for range sig {
			// 核心的 Upgrade 呼叫
			err := upg.Upgrade()
			if err != nil {
				log.Println("Upgrade failed:", err)
			}
		}
	})
}

func Ready() {
	if err := Upgrade.Ready(); err != nil {
		panic(err)
	}
	log.Println("idefav proxy is ready!")
}

func Stop(shutDownHook func()) {
	defer Upgrade.Stop()
	<-Upgrade.Exit()

	time.AfterFunc(30*time.Second, func() {
		log.Println("Graceful shutdown timed out")
		os.Exit(1)
	})
	if shutDownHook != nil {
		shutDownHook()
	}
}
