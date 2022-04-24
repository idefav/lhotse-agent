package proxy

import (
	"bufio"
	"errors"
	"fmt"
	"idefav-proxy/pkg/pool"
	"net"
	"time"
)

var (
	ParamError       = errors.New("参数错误")
	CreateTcpConnErr = errors.New("创建TCP连接失败")
)

func NewConnPool(host string, port int, coreSize int, maxSize int, maxIdleCount int) (pool.Pool, error) {
	if port <= 0 {
		return nil, ParamError
	}
	config := &pool.Config{
		InitialCap: coreSize,
		MaxCap:     maxSize,
		MaxIdle:    maxIdleCount,
		Factory: func() (*pool.ClientConn, error) {
			dial, err := net.Dial("tcp", fmt.Sprintf("%s:%d", host, port))
			reader := bufio.NewReader(dial)

			return &pool.ClientConn{
				C: &dial,
				R: reader,
			}, err
		},
		Close: func(conn *pool.ClientConn) error {
			return (*conn.C).Close()
		},
		Ping:        nil,
		IdleTimeout: 60 * time.Second,
	}
	return pool.NewChannelPool(config)
}
