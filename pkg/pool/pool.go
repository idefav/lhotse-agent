package pool

import (
	"bufio"
	"errors"
	"net"
	"sync"
	"time"
)

var (
	//ErrMaxActiveConnReached 连接池超限
	ErrMaxActiveConnReached = errors.New("MaxActiveConnReached")
)

// Config 连接池相关配置
type Config struct {
	//连接池中拥有的最小连接数
	InitialCap int
	//最大并发存活连接数
	MaxCap int
	//最大空闲连接
	MaxIdle int
	//生成连接的方法
	Factory func() (*ClientConn, error)
	//关闭连接的方法
	Close func(*ClientConn) error
	//检查连接是否有效的方法
	Ping func(*ClientConn) error
	//连接最大空闲时间，超过该事件则将失效
	IdleTimeout time.Duration
}

// channelPool 存放连接信息
type channelPool struct {
	mu                       sync.RWMutex
	idleConnections          chan *idleConn
	closeStatCheck           chan *idleConn
	factory                  func() (*ClientConn, error)
	close                    func(*ClientConn) error
	ping                     func(*ClientConn) error
	idleTimeout, waitTimeOut time.Duration
	maxActive                int
	openedCount              int
}

type idleConn struct {
	conn *ClientConn
	t    time.Time
}

type ClientConn struct {
	C      *net.Conn
	R      *bufio.Reader
	Closed bool
}

var (
	//ErrClosed 连接池已经关闭Error
	ErrClosed = errors.New("pool is closed")
)

// Pool 基本方法
type Pool interface {
	Get() (*ClientConn, error)

	Put(conn *ClientConn) error

	Close(conn *ClientConn) error

	Release()

	Len() int
}

// NewChannelPool 初始化连接
func NewChannelPool(poolConfig *Config) (Pool, error) {
	if !(poolConfig.InitialCap <= poolConfig.MaxIdle && poolConfig.MaxCap >= poolConfig.MaxIdle && poolConfig.InitialCap >= 0) {
		return nil, errors.New("invalid capacity settings")
	}
	if poolConfig.Factory == nil {
		return nil, errors.New("invalid factory func settings")
	}
	if poolConfig.Close == nil {
		return nil, errors.New("invalid close func settings")
	}

	c := &channelPool{
		idleConnections: make(chan *idleConn, poolConfig.MaxIdle),
		closeStatCheck:  make(chan *idleConn, poolConfig.MaxIdle),
		factory:         poolConfig.Factory,
		close:           poolConfig.Close,
		idleTimeout:     poolConfig.IdleTimeout,
		maxActive:       poolConfig.MaxCap,
		openedCount:     0,
	}

	if poolConfig.Ping != nil {
		c.ping = poolConfig.Ping
	}

	//for i := 0; i < poolConfig.InitialCap; i++ {
	//	conn, err := c.factory()
	//	if err != nil {
	//		c.Release()
	//		return nil, fmt.Errorf("factory is not able to fill the pool: %s", err)
	//	}
	//	c.idleConnections <- &idleConn{conn: conn, t: time.Now()}
	//}

	//go func() {
	//	for {
	//		select {
	//		case conn := <-c.closeStatCheck:
	//			go func() {
	//				t, err2 := conn.conn.R.Peek(12)
	//				if err2 != nil {
	//					log.Println("客户端连接关闭")
	//					c.Close(conn.conn)
	//				} else {
	//					log.Println("收到:" + string(t))
	//				}
	//			}()
	//		}
	//	}
	//}()

	return c, nil
}

// getConns 获取所有连接
func (c *channelPool) getConns() chan *idleConn {
	c.mu.Lock()
	conns := c.idleConnections
	c.mu.Unlock()
	return conns
}

// Get 从pool中取一个连接
func (c *channelPool) Get() (*ClientConn, error) {
	conns := c.getConns()
	if conns == nil {
		return nil, ErrClosed
	}
	for {
		select {
		case wrapConn := <-conns:
			if wrapConn == nil {
				return nil, ErrClosed
			}
			//判断是否超时，超时则丢弃
			if timeout := c.idleTimeout; timeout > 0 {
				if wrapConn.t.Add(timeout).Before(time.Now()) {
					//丢弃并关闭该连接
					c.Close(wrapConn.conn)
					continue
				}
			}
			//判断是否失效，失效则丢弃，如果用户没有设定 ping 方法，就不检查
			if c.ping != nil {
				if err := c.Ping(wrapConn.conn); err != nil {
					c.Close(wrapConn.conn)
					continue
				}
			}
			if wrapConn.conn.Closed {
				continue
			}
			//reader := bufio.NewReader(*wrapConn.conn.C)
			//wrapConn.conn.R = reader
			return wrapConn.conn, nil
		default:
			c.mu.Lock()
			defer c.mu.Unlock()
			if c.openedCount >= c.maxActive {
				return nil, ErrMaxActiveConnReached
			}
			if c.factory == nil {
				return nil, ErrClosed
			}
			conn, err := c.factory()
			if err != nil {
				return nil, err
			}
			//go func() {
			//	nc := *conn.C
			//	_, err2 := conn.R.Peek(4)
			//	if err2 != nil {
			//		log.Println("客户端连接关闭")
			//		nc.Close()
			//	}
			//}()

			c.openedCount++
			return conn, nil
		}
	}
}

// Put 将连接放回pool中
func (c *channelPool) Put(conn *ClientConn) error {
	if conn == nil {
		return errors.New("connection is nil. rejecting")
	}

	c.mu.Lock()
	cc := *conn.C
	cc.SetReadDeadline(time.Now().Add(c.idleTimeout))
	reader := bufio.NewReader(cc)
	conn.R = reader

	if c.idleConnections == nil {
		c.mu.Unlock()
		return c.Close(conn)
	}

	co := idleConn{conn: conn, t: time.Now()}

	select {
	case c.idleConnections <- &co:
		//c.closeStatCheck <- &co
		c.mu.Unlock()
		return nil
	default:
		c.mu.Unlock()
		//连接池已满，直接关闭该连接
		return c.Close(conn)
	}
}

// Close 关闭单条连接
func (c *channelPool) Close(conn *ClientConn) error {
	if conn == nil {
		return errors.New("connection is nil. rejecting")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.close == nil {
		return nil
	}
	c.openedCount--
	conn.Closed = true
	return c.close(conn)
}

// Ping 检查单条连接是否有效
func (c *channelPool) Ping(conn *ClientConn) error {
	if conn == nil {
		return errors.New("connection is nil. rejecting")
	}
	return c.ping(conn)
}

// Release 释放连接池中所有连接
func (c *channelPool) Release() {
	c.mu.Lock()
	conns := c.idleConnections
	c.idleConnections = nil
	c.factory = nil
	c.ping = nil
	closeFun := c.close
	c.close = nil
	c.mu.Unlock()

	if conns == nil {
		return
	}

	close(conns)
	for wrapConn := range conns {
		//log.Printf("Type %v\n",reflect.TypeOf(wrapConn.conn))
		closeFun(wrapConn.conn)
	}
}

// Len 连接池中已有的连接
func (c *channelPool) Len() int {
	return len(c.getConns())
}
